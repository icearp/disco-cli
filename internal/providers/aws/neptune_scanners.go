package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/neptune"
)

func init() {
	registerService(serviceEntry{
		name: "aws:neptune",
		fn:   scanNeptune,
		emits: []coverage.TypeDecl{
			{Service: "neptune", DiscoType: TypeNeptuneCluster},
			{Service: "neptune", DiscoType: TypeNeptuneInstance},
			{Service: "neptune", DiscoType: TypeNeptuneDBClusterParameterGroup},
			{Service: "neptune", DiscoType: TypeNeptuneDBParameterGroup},
			{Service: "neptune", DiscoType: TypeNeptuneDBSubnetGroup},
			{Service: "neptune", DiscoType: TypeNeptuneEventSubscription},
		},
	})
}

// neptuneAPI is the narrow set of Neptune operations called by the
// scanNeptune sub-phases.
type neptuneAPI interface {
	DescribeDBClusters(context.Context, *neptune.DescribeDBClustersInput, ...func(*neptune.Options)) (*neptune.DescribeDBClustersOutput, error)
	DescribeDBInstances(context.Context, *neptune.DescribeDBInstancesInput, ...func(*neptune.Options)) (*neptune.DescribeDBInstancesOutput, error)
	DescribeDBClusterParameterGroups(context.Context, *neptune.DescribeDBClusterParameterGroupsInput, ...func(*neptune.Options)) (*neptune.DescribeDBClusterParameterGroupsOutput, error)
	DescribeDBParameterGroups(context.Context, *neptune.DescribeDBParameterGroupsInput, ...func(*neptune.Options)) (*neptune.DescribeDBParameterGroupsOutput, error)
	DescribeDBSubnetGroups(context.Context, *neptune.DescribeDBSubnetGroupsInput, ...func(*neptune.Options)) (*neptune.DescribeDBSubnetGroupsOutput, error)
	DescribeEventSubscriptions(context.Context, *neptune.DescribeEventSubscriptionsInput, ...func(*neptune.Options)) (*neptune.DescribeEventSubscriptionsOutput, error)
}

// scanNeptune discovers Amazon Neptune clusters and instances in one
// region. Although Neptune rides on the RDS control-plane API
// (rds:DescribeDBClusters returns Engine=neptune rows), the dedicated
// neptune SDK service gives us proper aws:neptune:* type semantics
// and isolates Neptune from RDS-engine resolvers. The companion change
// in scanRDSClusters / scanRDSInstances filters Engine in {neptune,
// docdb} so rows aren't duplicated across types.
//
// Two phases run sequentially, both Describe* paginator-native with
// full body on List. Per-phase AccessDenied tolerated. Cluster
// snapshots, parameter groups, subnet groups, global clusters, and
// Neptune-specific resources (DescribeDBClusterEndpoints) deferred —
// same scope rationale as Redshift / DocumentDB.
func scanNeptune(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := neptune.NewFromConfig(acct.cfg, func(o *neptune.Options) { o.Region = region })

	{
		t, i, ferr := scanNeptuneClusters(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanNeptuneInstances(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanNeptuneExtended(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func scanNeptuneClusters(ctx context.Context, client neptuneAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := neptune.NewDescribeDBClustersPaginator(client, &neptune.DescribeDBClustersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "neptune:DescribeDBClusters", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("neptune:DescribeDBClusters: %w", perr)
		}
		for _, c := range out.DBClusters {
			arn := sv(c.DBClusterArn)
			if arn == "" {
				continue
			}
			name := sv(c.DBClusterIdentifier)
			status := sv(c.Status)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeNeptuneCluster,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert neptune clusters: %w", uerr)
	}
	return len(batch), n, nil
}

func scanNeptuneInstances(ctx context.Context, client neptuneAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := neptune.NewDescribeDBInstancesPaginator(client, &neptune.DescribeDBInstancesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "neptune:DescribeDBInstances", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("neptune:DescribeDBInstances: %w", perr)
		}
		for _, i := range out.DBInstances {
			arn := sv(i.DBInstanceArn)
			if arn == "" {
				continue
			}
			name := sv(i.DBInstanceIdentifier)
			status := sv(i.DBInstanceStatus)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeNeptuneInstance,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(i),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert neptune instances: %w", uerr)
	}
	return len(batch), n, nil
}
