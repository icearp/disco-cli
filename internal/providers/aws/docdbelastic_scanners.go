package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/docdbelastic"
)

func init() {
	registerService(serviceEntry{
		name: "aws:doc-db-elastic",
		fn:   scanDocDBElastic,
		emits: []coverage.TypeDecl{
			{Service: "doc-db-elastic", DiscoType: TypeDocDBElasticCluster},
			{Service: "doc-db-elastic", DiscoType: TypeDocDBElasticClusterSnapshot},
		},
	})
}

type docDBElasticAPI interface {
	ListClusters(context.Context, *docdbelastic.ListClustersInput, ...func(*docdbelastic.Options)) (*docdbelastic.ListClustersOutput, error)
	GetCluster(context.Context, *docdbelastic.GetClusterInput, ...func(*docdbelastic.Options)) (*docdbelastic.GetClusterOutput, error)
	ListClusterSnapshots(context.Context, *docdbelastic.ListClusterSnapshotsInput, ...func(*docdbelastic.Options)) (*docdbelastic.ListClusterSnapshotsOutput, error)
	GetClusterSnapshot(context.Context, *docdbelastic.GetClusterSnapshotInput, ...func(*docdbelastic.Options)) (*docdbelastic.GetClusterSnapshotOutput, error)
}

// scanDocDBElastic discovers DocumentDB Elastic clusters and their snapshots.
func scanDocDBElastic(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := docdbelastic.NewFromConfig(acct.cfg, func(o *docdbelastic.Options) { o.Region = region })
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDocDBElasticClusters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDocDBElasticSnapshots(ctx, client, acct, region, st, scanID) },
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

func scanDocDBElasticClusters(ctx context.Context, client docDBElasticAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListClusters(ctx, &docdbelastic.ListClustersInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "docdb-elastic:ListClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("docdb-elastic:ListClusters: %w", err)
		}
		for _, c := range out.Clusters {
			arn := sv(c.ClusterArn)
			if arn == "" {
				continue
			}
			status := string(c.Status)
			attrsJSON := mustJSON(c)
			if gout, gerr := client.GetCluster(ctx, &docdbelastic.GetClusterInput{ClusterArn: c.ClusterArn}); gerr == nil && gout.Cluster != nil {
				attrsJSON = mustJSON(gout.Cluster)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDocDBElasticCluster, NativeID: arn,
				Name: c.ClusterName, Region: &region, Status: &status,
				AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "docdb-elastic clusters")
}

func scanDocDBElasticSnapshots(ctx context.Context, client docDBElasticAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := docdbelastic.NewListClusterSnapshotsPaginator(client, &docdbelastic.ListClusterSnapshotsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "docdb-elastic:ListClusterSnapshots", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("docdb-elastic:ListClusterSnapshots: %w", err)
		}
		for _, s := range out.Snapshots {
			arn := sv(s.SnapshotArn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			attrsJSON := mustJSON(s)
			// Enrich for KMS/subnet/SG edges the list shape omits.
			if gout, gerr := client.GetClusterSnapshot(ctx, &docdbelastic.GetClusterSnapshotInput{SnapshotArn: s.SnapshotArn}); gerr == nil && gout.Snapshot != nil {
				attrsJSON = mustJSON(gout.Snapshot)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDocDBElasticClusterSnapshot, NativeID: arn,
				Name: s.SnapshotName, Region: &region, Status: &status,
				AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "docdb-elastic cluster-snapshots")
}
