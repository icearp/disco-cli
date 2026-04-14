package aws

import (
	"context"
	"sync/atomic"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"golang.org/x/sync/errgroup"
)

// scanEC2Observability discovers observability and policy types: flow logs,
// managed prefix lists, and all Network Insights resources.
func scanEC2Observability(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { tt, nn, e := scanFlowLogs(ctx, client, acct, region, st, scanID); add(tt, nn); return e })
	g.Go(func() error {
		tt, nn, e := scanPrefixLists(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanNetworkInsightsPaths(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanNetworkInsightsAnalyses(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanNetworkInsightsAccessScopes(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanNetworkInsightsAccessScopeAnalyses(ctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

func scanNetworkInsightsPaths(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeNetworkInsightsPaths", acct, region, st,
		ec2.NewDescribeNetworkInsightsPathsPaginator(client, &ec2.DescribeNetworkInsightsPathsInput{}),
		func(page *ec2.DescribeNetworkInsightsPathsOutput) []*store.Resource {
			var out []*store.Resource
			for _, p := range page.NetworkInsightsPaths {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2NetworkInsightsPath,
					NativeID:       sv(p.NetworkInsightsPathArn),
					Region:         &region,
					CreatedAt:      tp(p.CreatedDate),
					TagsJSON:       awsTagsJSON(p.Tags),
					AttributesJSON: mustJSON(p),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanNetworkInsightsAnalyses(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeNetworkInsightsAnalyses", acct, region, st,
		ec2.NewDescribeNetworkInsightsAnalysesPaginator(client, &ec2.DescribeNetworkInsightsAnalysesInput{}),
		func(page *ec2.DescribeNetworkInsightsAnalysesOutput) []*store.Resource {
			var out []*store.Resource
			for _, a := range page.NetworkInsightsAnalyses {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2NetworkInsightsAnalysis,
					NativeID:       sv(a.NetworkInsightsAnalysisArn),
					Region:         &region,
					CreatedAt:      tp(a.StartDate),
					TagsJSON:       awsTagsJSON(a.Tags),
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanNetworkInsightsAccessScopes(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeNetworkInsightsAccessScopes", acct, region, st,
		ec2.NewDescribeNetworkInsightsAccessScopesPaginator(client, &ec2.DescribeNetworkInsightsAccessScopesInput{}),
		func(page *ec2.DescribeNetworkInsightsAccessScopesOutput) []*store.Resource {
			var out []*store.Resource
			for _, s := range page.NetworkInsightsAccessScopes {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2NetworkInsightsAccessScope,
					NativeID:       sv(s.NetworkInsightsAccessScopeArn),
					Region:         &region,
					CreatedAt:      tp(s.CreatedDate),
					TagsJSON:       awsTagsJSON(s.Tags),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanNetworkInsightsAccessScopeAnalyses(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeNetworkInsightsAccessScopeAnalyses", acct, region, st,
		ec2.NewDescribeNetworkInsightsAccessScopeAnalysesPaginator(client, &ec2.DescribeNetworkInsightsAccessScopeAnalysesInput{}),
		func(page *ec2.DescribeNetworkInsightsAccessScopeAnalysesOutput) []*store.Resource {
			var out []*store.Resource
			for _, a := range page.NetworkInsightsAccessScopeAnalyses {
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2NetworkInsightsAccessScopeAnalysis,
					NativeID:       sv(a.NetworkInsightsAccessScopeAnalysisArn),
					Region:         &region,
					CreatedAt:      tp(a.StartDate),
					TagsJSON:       awsTagsJSON(a.Tags),
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanFlowLogs(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeFlowLogs", acct, region, st,
		ec2.NewDescribeFlowLogsPaginator(client, &ec2.DescribeFlowLogsInput{}),
		func(page *ec2.DescribeFlowLogsOutput) []*store.Resource {
			var out []*store.Resource
			for _, fl := range page.FlowLogs {
				status := sv(fl.FlowLogStatus)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2FlowLog,
					NativeID:       ec2ARN(region, acct.ID, "vpc-flow-log", sv(fl.FlowLogId)),
					Region:         &region,
					CreatedAt:      tp(fl.CreationTime),
					Status:         &status,
					TagsJSON:       awsTagsJSON(fl.Tags),
					AttributesJSON: mustJSON(fl),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}

func scanPrefixLists(ctx context.Context, client *ec2.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(ctx, "ec2:DescribeManagedPrefixLists", acct, region, st,
		ec2.NewDescribeManagedPrefixListsPaginator(client, &ec2.DescribeManagedPrefixListsInput{}),
		func(page *ec2.DescribeManagedPrefixListsOutput) []*store.Resource {
			var out []*store.Resource
			for _, pl := range page.PrefixLists {
				status := string(pl.State)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2PrefixList,
					NativeID:       sv(pl.PrefixListArn),
					Name:           pl.PrefixListName,
					Region:         &region,
					Status:         &status,
					TagsJSON:       awsTagsJSON(pl.Tags),
					AttributesJSON: mustJSON(pl),
					DiscoveredBy:   scanID,
				})
			}
			return out
		},
	)
}
