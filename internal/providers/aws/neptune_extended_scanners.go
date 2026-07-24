package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/neptune"
)

// scanNeptuneExtended discovers additional Neptune resource types: DB cluster
// parameter groups, DB parameter groups, event subscriptions, and global
// clusters. All carry native ARNs. DB subnet groups are intentionally not
// scanned here — they are shared RDS-family infra with an rds ARN and no engine
// field, so aws:rds:db-subnet-group owns them (re-reporting under
// aws:neptune:db-subnet-group would collide on identity, which excludes type).
func scanNeptuneExtended(ctx context.Context, client neptuneAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanNeptuneDBClusterPGs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanNeptuneDBPGs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanNeptuneEventSubs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanNeptuneGlobalClusters(ctx, client, acct, region, st, scanID) },
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
			// DescribeDBParameterGroups on the Neptune endpoint returns every
			// RDS-family parameter group (shared ARN namespace). Identity excludes
			// type, so a plain-Postgres group re-reported under
			// aws:neptune:db-parameter-group collides with its aws:rds row. Emit
			// only genuine Neptune families (e.g. "neptune1.3").
			if !strings.HasPrefix(sv(g.DBParameterGroupFamily), "neptune") {
				continue
			}
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

// scanNeptuneGlobalClusters discovers Neptune global database clusters via the
// dedicated neptune SDK (distinct from RDS Aurora global clusters). Global
// clusters aren't region-scoped; this runs per-region but UpsertResources
// dedupes by NativeID (GlobalClusterArn carries no region), so Region is left
// unset to avoid cross-region version churn.
func scanNeptuneGlobalClusters(ctx context.Context, client neptuneAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := neptune.NewDescribeGlobalClustersPaginator(client, &neptune.DescribeGlobalClustersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "neptune:DescribeGlobalClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("neptune:DescribeGlobalClusters: %w", err)
		}
		for _, g := range out.GlobalClusters {
			arn := sv(g.GlobalClusterArn)
			if arn == "" {
				continue
			}
			name := sv(g.GlobalClusterIdentifier)
			status := sv(g.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNeptuneGlobalCluster, NativeID: arn,
				Name: &name, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "neptune global-clusters")
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
