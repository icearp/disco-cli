package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/docdbelastic"
)

func init() {
	registerService(serviceEntry{
		name: "aws:doc-db-elastic",
		fn:   scanDocDBElastic,
		emits: []coverage.TypeDecl{
			{Service: "doc-db-elastic", DiscoType: TypeDocDBElasticCluster},
		},
	})
}

type docDBElasticAPI interface {
	ListClusters(context.Context, *docdbelastic.ListClustersInput, ...func(*docdbelastic.Options)) (*docdbelastic.ListClustersOutput, error)
	GetCluster(context.Context, *docdbelastic.GetClusterInput, ...func(*docdbelastic.Options)) (*docdbelastic.GetClusterOutput, error)
}

// scanDocDBElastic discovers DocumentDB Elastic clusters.
func scanDocDBElastic(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := docdbelastic.NewFromConfig(acct.cfg, func(o *docdbelastic.Options) { o.Region = region })

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
