package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/arcregionswitch"
)

func init() {
	registerType(restype.Descriptor{Type: TypeARCRegionSwitchPlan, Service: "arc-region-switch", Upstream: "AWS::ARCRegionSwitch::Plan"})
	registerService(serviceEntry{
		name: "aws:arc-region-switch",
		fn:   scanARCRegionSwitch,
	})
}

type arcRegionSwitchAPI interface {
	ListPlans(context.Context, *arcregionswitch.ListPlansInput, ...func(*arcregionswitch.Options)) (*arcregionswitch.ListPlansOutput, error)
}

func scanARCRegionSwitch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := arcregionswitch.NewFromConfig(acct.cfg, func(o *arcregionswitch.Options) { o.Region = region })
	return scanARCRegionSwitchPlans(ctx, client, acct, region, st, scanID)
}

func scanARCRegionSwitchPlans(ctx context.Context, client arcRegionSwitchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := arcregionswitch.NewListPlansPaginator(client, &arcregionswitch.ListPlansInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "arc-region-switch:ListPlans", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("arc-region-switch:ListPlans: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Plans))
		for _, pl := range page.Plans {
			arn := sv(pl.Arn)
			if arn == "" {
				continue
			}
			name := sv(pl.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeARCRegionSwitchPlan,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(pl),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert arc-region-switch plans: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
