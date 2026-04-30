package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/arczonalshift"
)

func init() {
	registerService(serviceEntry{
		name: "aws:arc-zonal-shift",
		fn:   scanARCZonalShift,
		emits: []coverage.TypeDecl{
			{Service: "arc-zonal-shift", DiscoType: TypeARCZonalShiftObserverStatus},
			{Service: "arc-zonal-shift", DiscoType: TypeARCZonalShiftConfiguration},
		},
	})
}

type arcZonalShiftAPI interface {
	GetAutoshiftObserverNotificationStatus(context.Context, *arczonalshift.GetAutoshiftObserverNotificationStatusInput, ...func(*arczonalshift.Options)) (*arczonalshift.GetAutoshiftObserverNotificationStatusOutput, error)
	ListManagedResources(context.Context, *arczonalshift.ListManagedResourcesInput, ...func(*arczonalshift.Options)) (*arczonalshift.ListManagedResourcesOutput, error)
}

// arcZonalShiftObserverNativeID synthesizes the NativeID for the per-(account,
// region) singleton autoshift-observer notification status. AWS exposes no
// ARN for this configuration; the get-only API returns just the Status enum.
func arcZonalShiftObserverNativeID(region, acct string) string {
	return fmt.Sprintf("arn:aws:arc-zonal-shift:%s:%s:autoshift-observer-notification-status", region, acct)
}

// arcZonalShiftConfigNativeID synthesizes the NativeID for the per-managed-
// resource zonal autoshift configuration. Identity is the managed resource
// ARN; the configuration is a sub-resource without its own ARN.
func arcZonalShiftConfigNativeID(managedResourceARN string) string {
	return managedResourceARN + "/zonal-autoshift-configuration"
}

func scanARCZonalShift(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := arczonalshift.NewFromConfig(acct.cfg, func(o *arczonalshift.Options) { o.Region = region })
	o, oi, err := scanARCZonalShiftObserver(ctx, client, acct, region, st, scanID)
	if err != nil {
		return o, oi, err
	}
	c, ci, err := scanARCZonalShiftConfigurations(ctx, client, acct, region, st, scanID)
	if err != nil {
		return o + c, oi + ci, err
	}
	return o + c, oi + ci, nil
}

func scanARCZonalShiftObserver(ctx context.Context, client arcZonalShiftAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetAutoshiftObserverNotificationStatus(ctx, &arczonalshift.GetAutoshiftObserverNotificationStatusInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "arc-zonal-shift:GetAutoshiftObserverNotificationStatus", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("arc-zonal-shift:GetAutoshiftObserverNotificationStatus: %w", err)
	}
	arn := arcZonalShiftObserverNativeID(region, acct.ID)
	name := "autoshift-observer-notification-status"
	status := string(out.Status)
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeARCZonalShiftObserverStatus,
		NativeID:       arn,
		Name:           &name,
		Region:         &region,
		Status:         &status,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}
	n, err := st.UpsertResources([]*store.Resource{r})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert arc-zonal-shift observer-status: %w", err)
	}
	return 1, n, nil
}

func scanARCZonalShiftConfigurations(ctx context.Context, client arcZonalShiftAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := arczonalshift.NewListManagedResourcesPaginator(client, &arczonalshift.ListManagedResourcesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "arc-zonal-shift:ListManagedResources", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("arc-zonal-shift:ListManagedResources: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Items))
		for _, m := range page.Items {
			mARN := sv(m.Arn)
			if mARN == "" {
				continue
			}
			// Only emit a configuration row when zonal autoshift or practice
			// run is actually configured for the resource — `LIST` returns
			// every managed-resource-eligible row (ECS, ALB, EC2 ASG).
			if string(m.ZonalAutoshiftStatus) == "" && string(m.PracticeRunStatus) == "" {
				continue
			}
			arn := arcZonalShiftConfigNativeID(mARN)
			name := sv(m.Name)
			status := string(m.ZonalAutoshiftStatus)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeARCZonalShiftConfiguration,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(m),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert arc-zonal-shift configurations: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
