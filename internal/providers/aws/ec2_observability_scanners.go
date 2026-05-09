package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2FlowLog},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2PrefixList, Leaf: true},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2NetworkInsightsPath},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2NetworkInsightsAnalysis},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2NetworkInsightsAccessScope, Leaf: true},
		coverage.TypeDecl{Service: "ec2", DiscoType: TypeEC2NetworkInsightsAccessScopeAnalysis},
	)
}

// scanEC2Observability discovers observability and policy types: flow logs,
// managed prefix lists, and all Network Insights resources.
func scanEC2Observability(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanFlowLogs(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanPrefixLists(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanNetworkInsightsPaths(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanNetworkInsightsAnalyses(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanNetworkInsightsAccessScopes(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanNetworkInsightsAccessScopeAnalyses(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanNetworkInsightsPaths(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeNetworkInsightsPaths", acct, region, st,
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

func scanNetworkInsightsAnalyses(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeNetworkInsightsAnalyses", acct, region, st,
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

func scanNetworkInsightsAccessScopes(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeNetworkInsightsAccessScopes", acct, region, st,
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

func scanNetworkInsightsAccessScopeAnalyses(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeNetworkInsightsAccessScopeAnalyses", acct, region, st,
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

func scanFlowLogs(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeFlowLogs", acct, region, st,
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

func scanPrefixLists(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return ec2PageScan(
		ctx, "ec2:DescribeManagedPrefixLists", acct, region, st,
		ec2.NewDescribeManagedPrefixListsPaginator(client, &ec2.DescribeManagedPrefixListsInput{}),
		func(page *ec2.DescribeManagedPrefixListsOutput) []*store.Resource {
			var out []*store.Resource
			for _, pl := range page.PrefixLists {
				status := string(pl.State)
				out = append(out, &store.Resource{
					Provider:          "aws",
					AccountID:         acct.ID,
					AccountName:       &acct.Name,
					Type:              TypeEC2PrefixList,
					NativeID:          sv(pl.PrefixListArn),
					Name:              pl.PrefixListName,
					Region:            &region,
					Status:            &status,
					TagsJSON:          awsTagsJSON(pl.Tags),
					AttributesJSON:    mustJSON(pl),
					DiscoveredBy:      scanID,
					ManagedByProvider: sv(pl.OwnerId) == "AWS",
				})
			}
			return out
		},
	)
}
