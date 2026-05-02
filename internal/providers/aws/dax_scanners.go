package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/dax"
)

func init() {
	registerService(serviceEntry{
		name: "aws:dax",
		fn:   scanDAX,
		emits: []coverage.TypeDecl{
			{Service: "dax", DiscoType: TypeDAXCluster},
			{Service: "dax", DiscoType: TypeDAXParameterGroup},
			{Service: "dax", DiscoType: TypeDAXSubnetGroup},
		},
	})
}

type daxAPI interface {
	DescribeClusters(context.Context, *dax.DescribeClustersInput, ...func(*dax.Options)) (*dax.DescribeClustersOutput, error)
	DescribeParameterGroups(context.Context, *dax.DescribeParameterGroupsInput, ...func(*dax.Options)) (*dax.DescribeParameterGroupsOutput, error)
	DescribeSubnetGroups(context.Context, *dax.DescribeSubnetGroupsInput, ...func(*dax.Options)) (*dax.DescribeSubnetGroupsOutput, error)
}

// scanDAX discovers DynamoDB Accelerator clusters, parameter groups, and
// subnet groups. ParameterGroup + SubnetGroup carry no native ARN; synth.
func scanDAX(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := dax.NewFromConfig(acct.cfg, func(o *dax.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDAXClusters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDAXParameterGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDAXSubnetGroups(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanDAXClusters(ctx context.Context, client daxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeClusters(ctx, &dax.DescribeClustersInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "dax:DescribeClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("dax:DescribeClusters: %w", err)
		}
		for _, c := range out.Clusters {
			arn := sv(c.ClusterArn)
			if arn == "" {
				continue
			}
			status := sv(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDAXCluster, NativeID: arn,
				Name: c.ClusterName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "dax clusters")
}

func scanDAXParameterGroups(ctx context.Context, client daxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeParameterGroups(ctx, &dax.DescribeParameterGroupsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "dax:DescribeParameterGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("dax:DescribeParameterGroups: %w", err)
		}
		for _, p := range out.ParameterGroups {
			name := sv(p.ParameterGroupName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:dax:%s:%s:parameter-group/%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDAXParameterGroup, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "dax parameter-groups")
}

func scanDAXSubnetGroups(ctx context.Context, client daxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeSubnetGroups(ctx, &dax.DescribeSubnetGroupsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "dax:DescribeSubnetGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("dax:DescribeSubnetGroups: %w", err)
		}
		for _, s := range out.SubnetGroups {
			name := sv(s.SubnetGroupName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:dax:%s:%s:subnet-group/%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDAXSubnetGroup, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "dax subnet-groups")
}
