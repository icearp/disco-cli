package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/docdb"
)

func init() {
	registerService(serviceEntry{
		name: "aws:docdb",
		fn:   scanDocDB,
		emits: []coverage.TypeDecl{
			{Service: "docdb", DiscoType: TypeDocDBCluster},
			{Service: "docdb", DiscoType: TypeDocDBInstance},
		},
	})
}

// docdbAPI is the narrow set of DocumentDB operations called by the
// scanDocDB sub-phases.
type docdbAPI interface {
	DescribeDBClusters(context.Context, *docdb.DescribeDBClustersInput, ...func(*docdb.Options)) (*docdb.DescribeDBClustersOutput, error)
	DescribeDBInstances(context.Context, *docdb.DescribeDBInstancesInput, ...func(*docdb.Options)) (*docdb.DescribeDBInstancesOutput, error)
}

// scanDocDB discovers Amazon DocumentDB clusters and instances in one
// region. DocumentDB has its own dedicated control-plane API (not shared
// with RDS, despite the structural similarity), so it needs a dedicated
// scanner. Two phases run sequentially, both paginator-native with full
// body on List. Per-phase AccessDenied tolerated. Cluster snapshots,
// parameter groups, subnet groups, and global clusters deferred — same
// scope rationale as Redshift.
func scanDocDB(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := docdb.NewFromConfig(acct.cfg, func(o *docdb.Options) { o.Region = region })

	if t, i, ferr := scanDocDBClusters(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanDocDBInstances(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func scanDocDBClusters(ctx context.Context, client docdbAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := docdb.NewDescribeDBClustersPaginator(client, &docdb.DescribeDBClustersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "docdb:DescribeDBClusters", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("docdb:DescribeDBClusters: %w", perr)
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
				Type:           TypeDocDBCluster,
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
		return 0, 0, fmt.Errorf("upsert docdb clusters: %w", uerr)
	}
	return len(batch), n, nil
}

func scanDocDBInstances(ctx context.Context, client docdbAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := docdb.NewDescribeDBInstancesPaginator(client, &docdb.DescribeDBInstancesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "docdb:DescribeDBInstances", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("docdb:DescribeDBInstances: %w", perr)
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
				Type:           TypeDocDBInstance,
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
		return 0, 0, fmt.Errorf("upsert docdb instances: %w", uerr)
	}
	return len(batch), n, nil
}
