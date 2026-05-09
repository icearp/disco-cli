package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/neptune"
)

// scanNeptuneExtended discovers four additional Neptune resource types:
// DB cluster parameter groups, DB parameter groups, DB subnet groups, and
// event subscriptions. All four carry native ARNs.
func scanNeptuneExtended(ctx context.Context, client neptuneAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanNeptuneDBClusterPGs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanNeptuneDBPGs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanNeptuneDBSubnetGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanNeptuneEventSubs(ctx, client, acct, region, st, scanID) },
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

func scanNeptuneDBClusterPGs(ctx context.Context, client neptuneAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := neptune.NewDescribeDBClusterParameterGroupsPaginator(client, &neptune.DescribeDBClusterParameterGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "neptune:DescribeDBClusterParameterGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("neptune:DescribeDBClusterParameterGroups: %w", err)
		}
		for _, g := range out.DBClusterParameterGroups {
			arn := sv(g.DBClusterParameterGroupArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNeptuneDBClusterParameterGroup, NativeID: arn,
				Name: g.DBClusterParameterGroupName, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "neptune db-cluster-parameter-groups")
}

func scanNeptuneDBPGs(ctx context.Context, client neptuneAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := neptune.NewDescribeDBParameterGroupsPaginator(client, &neptune.DescribeDBParameterGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "neptune:DescribeDBParameterGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("neptune:DescribeDBParameterGroups: %w", err)
		}
		for _, g := range out.DBParameterGroups {
			arn := sv(g.DBParameterGroupArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNeptuneDBParameterGroup, NativeID: arn,
				Name: g.DBParameterGroupName, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "neptune db-parameter-groups")
}

func scanNeptuneDBSubnetGroups(ctx context.Context, client neptuneAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := neptune.NewDescribeDBSubnetGroupsPaginator(client, &neptune.DescribeDBSubnetGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "neptune:DescribeDBSubnetGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("neptune:DescribeDBSubnetGroups: %w", err)
		}
		for _, g := range out.DBSubnetGroups {
			arn := sv(g.DBSubnetGroupArn)
			if arn == "" {
				continue
			}
			status := sv(g.SubnetGroupStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNeptuneDBSubnetGroup, NativeID: arn,
				Name: g.DBSubnetGroupName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "neptune db-subnet-groups")
}

func scanNeptuneEventSubs(ctx context.Context, client neptuneAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := neptune.NewDescribeEventSubscriptionsPaginator(client, &neptune.DescribeEventSubscriptionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "neptune:DescribeEventSubscriptions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("neptune:DescribeEventSubscriptions: %w", err)
		}
		for _, s := range out.EventSubscriptionsList {
			arn := sv(s.EventSubscriptionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNeptuneEventSubscription, NativeID: arn,
				Name: s.CustSubscriptionId, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "neptune event-subscriptions")
}
