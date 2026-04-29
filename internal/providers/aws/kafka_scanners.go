package aws

import (
	"context"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:kafka",
		fn:   scanKafka,
		emits: []coverage.TypeDecl{
			{Service: "msk", DiscoType: TypeMSKCluster},
		},
	})
}

// kafkaAPI is the narrow set of MSK operations called by scanKafkaClusters.
type kafkaAPI interface {
	ListClustersV2(context.Context, *kafka.ListClustersV2Input, ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error)
}

// scanKafka discovers MSK clusters (both Provisioned and Serverless) in one
// region. ListClustersV2 returns the full Cluster object per entry — no
// separate Describe is needed. The Provisioned and Serverless variants are
// both carried in AttributesJSON as returned by the SDK; resolvers branch on
// which sub-struct is populated.
func scanKafka(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := kafka.NewFromConfig(acct.cfg, func(o *kafka.Options) { o.Region = region })
	return scanKafkaClusters(ctx, client, acct, region, st, scanID)
}

// scanKafkaClusters holds the testable scan body.
func scanKafkaClusters(ctx context.Context, client kafkaAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := kafka.NewListClustersV2Paginator(client, &kafka.ListClustersV2Input{})
	return pageScan(ctx, "kafka:ListClustersV2", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*kafka.ListClustersV2Output, error) { return p.NextPage(c) },
		func(o *kafka.ListClustersV2Output) []kafkatypes.Cluster { return o.ClusterInfoList },
		func(c kafkatypes.Cluster) *store.Resource {
			state := string(c.State)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeMSKCluster,
				NativeID:       sv(c.ClusterArn),
				Name:           c.ClusterName,
				Region:         &region,
				CreatedAt:      tp(c.CreationTime),
				Status:         &state,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			}
			if len(c.Tags) > 0 {
				s := mustJSON(c.Tags)
				r.TagsJSON = &s
			}
			return r
		})
}
