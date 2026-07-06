package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/docdb"
)

// scanDocDBExtended discovers four additional DocumentDB resource types: DB
// cluster parameter groups, DB subnet groups, event subscriptions, global
// clusters. ARNs native on every type. DescribeGlobalClusters has no
// paginator — manual Marker loop.
func scanDocDBExtended(ctx context.Context, client docdbAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDocDBClusterPGs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDocDBSubnetGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDocDBEventSubs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDocDBGlobalClusters(ctx, client, acct, region, st, scanID) },
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

func scanDocDBClusterPGs(ctx context.Context, client docdbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := docdb.NewDescribeDBClusterParameterGroupsPaginator(client, &docdb.DescribeDBClusterParameterGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "docdb:DescribeDBClusterParameterGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("docdb:DescribeDBClusterParameterGroups: %w", err)
		}
		for _, g := range out.DBClusterParameterGroups {
			arn := sv(g.DBClusterParameterGroupArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDocDBDBClusterParameterGroup, NativeID: arn,
				Name: g.DBClusterParameterGroupName, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
				// AWS-supplied default groups are named "default.<engine-version>"
				// (e.g. "default.docdb5.0"); customer groups carry user-chosen names.
				ManagedByProvider: strings.HasPrefix(sv(g.DBClusterParameterGroupName), "default."),
			})
		}
	}
	return upsertBatch(st, batch, "docdb db-cluster-parameter-groups")
}

func scanDocDBSubnetGroups(ctx context.Context, client docdbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := docdb.NewDescribeDBSubnetGroupsPaginator(client, &docdb.DescribeDBSubnetGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "docdb:DescribeDBSubnetGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("docdb:DescribeDBSubnetGroups: %w", err)
		}
		for _, g := range out.DBSubnetGroups {
			arn := sv(g.DBSubnetGroupArn)
			if arn == "" {
				continue
			}
			status := sv(g.SubnetGroupStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDocDBDBSubnetGroup, NativeID: arn,
				Name: g.DBSubnetGroupName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "docdb db-subnet-groups")
}

func scanDocDBEventSubs(ctx context.Context, client docdbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := docdb.NewDescribeEventSubscriptionsPaginator(client, &docdb.DescribeEventSubscriptionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "docdb:DescribeEventSubscriptions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("docdb:DescribeEventSubscriptions: %w", err)
		}
		for _, s := range out.EventSubscriptionsList {
			arn := sv(s.EventSubscriptionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDocDBEventSubscription, NativeID: arn,
				Name: s.CustSubscriptionId, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "docdb event-subscriptions")
}

// scanDocDBGlobalClusters uses manual Marker pagination — the SDK exposes no
// paginator constructor for DescribeGlobalClusters.
func scanDocDBGlobalClusters(ctx context.Context, client docdbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var marker *string
	for {
		out, err := client.DescribeGlobalClusters(ctx, &docdb.DescribeGlobalClustersInput{Marker: marker})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "docdb:DescribeGlobalClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("docdb:DescribeGlobalClusters: %w", err)
		}
		for _, g := range out.GlobalClusters {
			arn := sv(g.GlobalClusterArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDocDBGlobalCluster, NativeID: arn,
				Name: g.GlobalClusterIdentifier, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
		if out.Marker == nil || *out.Marker == "" {
			break
		}
		marker = out.Marker
	}
	return upsertBatch(st, batch, "docdb global-clusters")
}
