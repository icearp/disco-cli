package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
)

func init() { registerService(serviceEntry{name: "aws:kafka", fn: scanKafka}) }

// scanKafka discovers MSK clusters (both Provisioned and Serverless) in one
// region. ListClustersV2 returns the full Cluster object per entry — no
// separate Describe is needed. The Provisioned and Serverless variants are
// both carried in AttributesJSON as returned by the SDK; resolvers branch on
// which sub-struct is populated.
func scanKafka(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kafka.NewFromConfig(acct.cfg, func(o *kafka.Options) { o.Region = region })

	pager := kafka.NewListClustersV2Paginator(client, &kafka.ListClustersV2Input{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kafka:ListClustersV2", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kafka:ListClustersV2: %w", err)
		}

		var batch []*store.Resource
		for _, c := range page.ClusterInfoList {
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
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert MSK clusters: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
