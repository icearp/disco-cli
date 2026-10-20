package aws

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMSKCluster, Service: "msk"})
	registerType(restype.Descriptor{Type: TypeMSKBatchScramSecret, Service: "msk"})
	registerType(restype.Descriptor{Type: TypeMSKClusterPolicy, Service: "msk"})
	registerType(restype.Descriptor{Type: TypeMSKConfiguration, Service: "msk", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeMSKReplicator, Service: "msk"})
	registerType(restype.Descriptor{Type: TypeMSKVpcConnection, Service: "msk"})
	registerType(restype.Descriptor{Type: TypeMSKTopic, Service: "msk", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeMSKChannel, Service: "msk"})
	registerType(restype.Descriptor{Type: TypeMSKClientVpcConnection, Service: "msk"})
	registerType(restype.Descriptor{Type: TypeMSKNode, Service: "msk"})
	registerService(serviceEntry{
		name: "aws:kafka",
		fn:   scanKafka,
	})
}

// kafkaAPI is the narrow set of MSK operations called by scanKafkaClusters.
type kafkaAPI interface {
	ListClustersV2(context.Context, *kafka.ListClustersV2Input, ...func(*kafka.Options)) (*kafka.ListClustersV2Output, error)
}

// kafkaAllAPI is every MSK operation scanKafka calls.
type kafkaAllAPI interface {
	kafkaAPI
	kafkaExtAPI
	kafkaChildAPI
}

// kafkaCluster is a cluster stored this scan, the parent of the per-cluster
// phases.
type kafkaCluster struct {
	arn         string
	clusterType kafkatypes.ClusterType
	// express is a provisioned cluster of Express brokers, the only kind
	// CreateChannel accepts.
	express bool
}

// scanKafka discovers MSK clusters (Provisioned and Serverless) in one
// region. ListClustersV2 returns the full Cluster object per entry — no
// separate Describe needed. Both variants are carried in AttributesJSON as
// returned by the SDK; resolvers branch on which sub-struct is populated.
func scanKafka(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := kafka.NewFromConfig(acct.cfg, func(o *kafka.Options) { o.Region = region })
	return scanKafkaAll(ctx, client, acct, region, st, scanID)
}

// scanKafkaAll runs the older phases, then the cluster child phases over the
// clusters they stored, each independent of the others. A failed older phase
// after the cluster phase costs the child phases nothing, and theirs cost it
// nothing; a failed cluster phase leaves the child phases only the clusters
// stored before it failed (see scanKafkaClusters). The child phases tally
// their transient errors per cluster, so only the older phases' error needs
// demoting before the join.
func scanKafkaAll(ctx context.Context, client kafkaAllAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, clusters, err := scanKafkaOlderPhases(ctx, client, acct, region, st, scanID)
	tt, it, terr := scanKafkaTopics(ctx, client, acct, region, st, scanID, clusters)
	tc, ic, cerr := scanKafkaChannels(ctx, client, acct, region, st, scanID, clusters)
	tv, iv, verr := scanKafkaClientVpcConnections(ctx, client, acct, region, st, scanID, clusters)
	tn, in, nerr := scanKafkaNodes(ctx, client, acct, region, st, scanID, clusters)
	return total + tt + tc + tv + tn, inserted + it + ic + iv + in,
		errors.Join(demoteTransient(st, "kafka", acct, region, err), terr, cerr, verr, nerr)
}

// scanKafkaOlderPhases runs the phases that predate the cluster child phases,
// stopping at the first hard error. It returns the clusters scanKafkaClusters
// returned, which are all stored this scan.
func scanKafkaOlderPhases(ctx context.Context, client kafkaAllAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, clusters []kafkaCluster, err error) {
	total, inserted, clusters, err = scanKafkaClusters(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, clusters, err
	}

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanKafkaConfigurations(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanKafkaReplicators(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanKafkaVpcConnections(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, clusters, perr
		}
		total += t
		inserted += i
	}
	for _, c := range clusters {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanKafkaClusterPolicy(ctx, client, acct, region, st, scanID, c.arn) },
			func() (int, int, error) { return scanKafkaScramSecrets(ctx, client, acct, region, st, scanID, c.arn) },
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, clusters, perr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, clusters, nil
}

// scanKafkaClusters stores the region's clusters and returns them as the
// parents of the per-cluster phases, keeping every cluster returned a row
// stored this scan. A failed page fetch adds no cluster, so on that error the
// clusters of the earlier, stored pages are returned with it; a failed write
// may have left the page's clusters unstored, so then none are returned.
func scanKafkaClusters(ctx context.Context, client kafkaAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []kafkaCluster, error) {
	var clusters []kafkaCluster
	p := kafka.NewListClustersV2Paginator(client, &kafka.ListClustersV2Input{})
	total, inserted, err := pageScan(ctx, "kafka:ListClustersV2", acct, region, st,
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
			if arn := sv(c.ClusterArn); arn != "" {
				clusters = append(clusters, kafkaCluster{arn: arn, clusterType: c.ClusterType, express: isKafkaExpress(c)})
			}
			return r
		})
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, nil, err
	}
	return total, inserted, clusters, err
}

// isKafkaExpress reports whether c runs Express brokers. The MSK
// documentation names Express broker instance types with an "express."
// prefix (express.m7g.large, …); the Cluster shape carries no broker-kind
// field, so the prefix is the only signal.
func isKafkaExpress(c kafkatypes.Cluster) bool {
	if c.Provisioned == nil || c.Provisioned.BrokerNodeGroupInfo == nil {
		return false
	}
	return strings.HasPrefix(sv(c.Provisioned.BrokerNodeGroupInfo.InstanceType), "express.")
}
