package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// scanEC2TrafficMirror discovers all Traffic Mirror resources in parallel.
func scanEC2TrafficMirror(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(ctx,
		func(ctx context.Context) (int, int, error) {
			return scanTrafficMirrorFilters(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanTrafficMirrorFilterRules(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanTrafficMirrorSessions(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanTrafficMirrorTargets(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanTrafficMirrorFilters(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeTrafficMirrorFilters", acct, region, st,
		ec2.NewDescribeTrafficMirrorFiltersPaginator(client, &ec2.DescribeTrafficMirrorFiltersInput{}),
		func(page *ec2.DescribeTrafficMirrorFiltersOutput) []*store.Resource {
			var out []*store.Resource
			for _, f := range page.TrafficMirrorFilters {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TrafficMirrorFilter,
					NativeID:       ec2ARN(region, acct.ID, "traffic-mirror-filter", sv(f.TrafficMirrorFilterId)),
					Region:         &region,
					TagsJSON:       awsTagsJSON(f.Tags),
					AttributesJSON: mustJSON(f),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

// scanTrafficMirrorFilterRules has no paginator in the SDK; use a direct call.
func scanTrafficMirrorFilterRules(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, err := client.DescribeTrafficMirrorFilterRules(ctx, &ec2.DescribeTrafficMirrorFilterRulesInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("ec2:DescribeTrafficMirrorFilterRules", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("ec2:DescribeTrafficMirrorFilterRules: %w", err)
	}
	var batch []*store.Resource
	for _, rule := range out.TrafficMirrorFilterRules {
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2TrafficMirrorFilterRule,
			NativeID:       ec2ARN(region, acct.ID, "traffic-mirror-filter-rule", sv(rule.TrafficMirrorFilterRuleId)),
			Region:         &region,
			TagsJSON:       awsTagsJSON(rule.Tags),
			AttributesJSON: mustJSON(rule),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert traffic-mirror-filter-rules: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

func scanTrafficMirrorSessions(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeTrafficMirrorSessions", acct, region, st,
		ec2.NewDescribeTrafficMirrorSessionsPaginator(client, &ec2.DescribeTrafficMirrorSessionsInput{}),
		func(page *ec2.DescribeTrafficMirrorSessionsOutput) []*store.Resource {
			var out []*store.Resource
			for _, s := range page.TrafficMirrorSessions {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TrafficMirrorSession,
					NativeID:       ec2ARN(region, acct.ID, "traffic-mirror-session", sv(s.TrafficMirrorSessionId)),
					Region:         &region,
					TagsJSON:       awsTagsJSON(s.Tags),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanTrafficMirrorTargets(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeTrafficMirrorTargets", acct, region, st,
		ec2.NewDescribeTrafficMirrorTargetsPaginator(client, &ec2.DescribeTrafficMirrorTargetsInput{}),
		func(page *ec2.DescribeTrafficMirrorTargetsOutput) []*store.Resource {
			var out []*store.Resource
			for _, t := range page.TrafficMirrorTargets {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2TrafficMirrorTarget,
					NativeID:       ec2ARN(region, acct.ID, "traffic-mirror-target", sv(t.TrafficMirrorTargetId)),
					Region:         &region,
					TagsJSON:       awsTagsJSON(t.Tags),
					AttributesJSON: mustJSON(t),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
