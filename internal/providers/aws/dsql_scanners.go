package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/dsql"
)

func init() {
	registerService(serviceEntry{
		name: "aws:dsql",
		fn:   scanDSQL,
		emits: []coverage.TypeDecl{
			{Service: "dsql", DiscoType: TypeDSQLCluster},
		},
	})
}

type dsqlAPI interface {
	ListClusters(context.Context, *dsql.ListClustersInput, ...func(*dsql.Options)) (*dsql.ListClustersOutput, error)
}

// scanDSQL discovers Aurora DSQL clusters.
func scanDSQL(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := dsql.NewFromConfig(acct.cfg, func(o *dsql.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListClusters(ctx, &dsql.ListClustersInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "dsql:ListClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("dsql:ListClusters: %w", err)
		}
		for _, c := range out.Clusters {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Identifier)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDSQLCluster, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "dsql clusters")
}
