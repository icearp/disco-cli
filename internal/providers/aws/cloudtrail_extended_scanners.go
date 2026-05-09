package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
)

// scanCloudTrailExtended discovers Lake channels, dashboards, and
// resource-based policies attached to channels/dashboards/event-data-stores.
// ResourcePolicy synth ARN: {targetArn}/policy.
func scanCloudTrailExtended(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	channelArns, t, i, ferr := scanCTChannels(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	dashboardArns, t, i, ferr := scanCTDashboards(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	edsArns, ferr := scanCTEDSARNs(ctx, client, acct, region, st)
	if ferr != nil {
		return total, inserted, ferr
	}

	policyTargets := append(append(channelArns, dashboardArns...), edsArns...)
	t, i, ferr = scanCTResourcePolicies(ctx, client, policyTargets, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanCTChannels(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListChannels(ctx, &cloudtrail.ListChannelsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "cloudtrail:ListChannels", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("cloudtrail:ListChannels: %w", err)
		}
		for _, c := range out.Channels {
			arn := sv(c.ChannelArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			detail, dErr := client.GetChannel(ctx, &cloudtrail.GetChannelInput{Channel: &arn})
			var attrs any = c
			if dErr == nil {
				attrs = detail
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudTrailChannel, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "cloudtrail channels")
	return arns, t, i, err
}

func scanCTDashboards(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListDashboards(ctx, &cloudtrail.ListDashboardsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "cloudtrail:ListDashboards", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("cloudtrail:ListDashboards: %w", err)
		}
		for _, d := range out.Dashboards {
			arn := sv(d.DashboardArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			detail, dErr := client.GetDashboard(ctx, &cloudtrail.GetDashboardInput{DashboardId: &arn})
			var attrs any = d
			var status string
			if dErr == nil {
				attrs = detail
				status = string(detail.Status)
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudTrailDashboard, NativeID: arn,
				Region:         &region,
				AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
			}
			if status != "" {
				r.Status = &status
			}
			batch = append(batch, r)
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "cloudtrail dashboards")
	return arns, t, i, err
}

// scanCTEDSARNs collects scanned event-data-store ARNs (already upserted by
// the main scanner) for resource-policy fan-out.
func scanCTEDSARNs(ctx context.Context, client cloudtrailAPI, acct *account, region string, st *store.Store) ([]string, error) {
	var arns []string
	var nextToken *string
	for {
		out, err := client.ListEventDataStores(ctx, &cloudtrail.ListEventDataStoresInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("cloudtrail:ListEventDataStores(policy): %w", err)
		}
		for _, e := range out.EventDataStores {
			if a := sv(e.EventDataStoreArn); a != "" {
				arns = append(arns, a)
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return arns, nil
}

func scanCTResourcePolicies(ctx context.Context, client cloudtrailAPI, targets []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, target := range targets {
		t := target
		out, err := client.GetResourcePolicy(ctx, &cloudtrail.GetResourcePolicyInput{ResourceArn: &t})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "ResourcePolicyNotFoundException", "ResourceNotFoundException", "ResourceTypeNotSupportedException") {
				continue
			}
			return 0, 0, fmt.Errorf("cloudtrail:GetResourcePolicy %s: %w", t, err)
		}
		if sv(out.ResourcePolicy) == "" && sv(out.DelegatedAdminResourcePolicy) == "" {
			continue
		}
		arn := t + "/policy"
		label := t
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeCloudTrailResourcePolicy, NativeID: arn,
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "cloudtrail resource-policies")
}
