package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dax"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDAXCluster, Service: "dax", Upstream: "AWS::DAX::Cluster"})
	registerType(restype.Descriptor{Type: TypeDAXParameterGroup, Service: "dax", Upstream: "AWS::DAX::ParameterGroup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeDAXSubnetGroup, Service: "dax", Upstream: "AWS::DAX::SubnetGroup"})
	registerService(serviceEntry{
		name: "aws:dax",
		fn:   scanDAX,
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
			// Regions without the DAX V3 control plane reject DescribeClusters with
			// InvalidParameterValueException "Access Denied to API Version: DAX_V3".
			// Per-region availability gap, not a real denial — silent-skip.
			if isAPIErrorWithMessage(err, "InvalidParameterValueException", "Access Denied to API Version") {
				return 0, 0, nil
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
			// Same DAX V3 control-plane gap DescribeClusters guards above — the
			// region rejects the call with InvalidParameterValueException rather
			// than an access-denied code. Silent-skip, don't fail the scan.
			if isAPIErrorWithMessage(err, "InvalidParameterValueException", "Access Denied to API Version") {
				return 0, 0, nil
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
				// AWS-supplied default parameter groups are named "default.<engine>"
				// (e.g. "default.dax.1.0"); customer groups carry user-chosen names.
				ManagedByProvider: strings.HasPrefix(name, "default."),
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
			// Same DAX V3 control-plane gap DescribeClusters guards above.
			if isAPIErrorWithMessage(err, "InvalidParameterValueException", "Access Denied to API Version") {
				return 0, 0, nil
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
