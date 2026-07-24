package aws

import (
	"context"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMSKCluster, Service: "msk", Upstream: "AWS::MSK::Cluster"})
	registerType(restype.Descriptor{Type: TypeMSKBatchScramSecret, Service: "msk", Upstream: "AWS::MSK::BatchScramSecret"})
	registerType(restype.Descriptor{Type: TypeMSKClusterPolicy, Service: "msk", Upstream: "AWS::MSK::ClusterPolicy"})
	registerType(restype.Descriptor{Type: TypeMSKConfiguration, Service: "msk", Upstream: "AWS::MSK::Configuration", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMSKReplicator, Service: "msk", Upstream: "AWS::MSK::Replicator"})
	registerType(restype.Descriptor{Type: TypeMSKVpcConnection, Service: "msk", Upstream: "AWS::MSK::VpcConnection"})
	registerService(serviceEntry{
		name: "aws:kafka",
		fn:   scanKafka,
	})
}

// kafkaAPI is the narrow set of MSK operations called by scanKafkaClusters.
type kafkaAPI interface {
	ListClustersV2(context.Context, *kafka.ListClustersV2Input, ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error)
}

// scanKafka discovers MSK clusters (Provisioned and Serverless) in one
// region. ListClustersV2 returns the full Cluster object per entry — no
// separate Describe needed. Both variants are carried in AttributesJSON as
// returned by the SDK; resolvers branch on which sub-struct is populated.
func scanKafka(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kafka.NewFromConfig(acct.cfg, func(o *kafka.Options) { o.Region = region })

	clusterARNs, t, i, ferr := scanKafkaClustersAndCollect(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanKafkaConfigurations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanKafkaReplicators(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanKafkaVpcConnections(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	for _, ca := range clusterARNs {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanKafkaClusterPolicy(ctx, client, acct, region, st, scanID, ca) },
			func() (int, int, error) { return scanKafkaScramSecrets(ctx, client, acct, region, st, scanID, ca) },
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

// scanKafkaClustersAndCollect wraps scanKafkaClusters and also returns the
// cluster ARN list for downstream per-cluster fan-out (cluster-policy,
// scram-secrets).
func scanKafkaClustersAndCollect(ctx context.Context, client kafkaAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	t, i, err := scanKafkaClusters(ctx, client, acct, region, st, scanID)
	if err != nil {
		return nil, t, i, err
	}
	rs, lerr := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeMSKCluster},
		Regions:   []string{region},
	})
	if lerr != nil {
		return nil, t, i, nil
	}
	arns := make([]string, 0, len(rs))
	for _, r := range rs {
		arns = append(arns, r.NativeID)
	}
	return arns, t, i, nil
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
