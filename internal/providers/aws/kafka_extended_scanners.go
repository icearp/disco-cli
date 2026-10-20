package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

type kafkaExtAPI interface {
	ListConfigurations(context.Context, *kafka.ListConfigurationsInput, ...func(*kafka.Options)) (*kafka.ListConfigurationsOutput, error)
	ListReplicators(context.Context, *kafka.ListReplicatorsInput, ...func(*kafka.Options)) (*kafka.ListReplicatorsOutput, error)
	ListVpcConnections(context.Context, *kafka.ListVpcConnectionsInput, ...func(*kafka.Options)) (*kafka.ListVpcConnectionsOutput, error)
	ListScramSecrets(context.Context, *kafka.ListScramSecretsInput, ...func(*kafka.Options)) (*kafka.ListScramSecretsOutput, error)
	GetClusterPolicy(context.Context, *kafka.GetClusterPolicyInput, ...func(*kafka.Options)) (*kafka.GetClusterPolicyOutput, error)
}

func scanKafkaConfigurations(ctx context.Context, client kafkaExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := kafka.NewListConfigurationsPaginator(client, &kafka.ListConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "kafka:ListConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("kafka:ListConfigurations: %w", perr)
		}
		for _, c := range out.Configurations {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMSKConfiguration, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kafka configurations")
}

func scanKafkaReplicators(ctx context.Context, client kafkaExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := kafka.NewListReplicatorsPaginator(client, &kafka.ListReplicatorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "kafka:ListReplicators", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("kafka:ListReplicators: %w", perr)
		}
		for _, r := range out.Replicators {
			arn := sv(r.ReplicatorArn)
			if arn == "" {
				continue
			}
			label := sv(r.ReplicatorName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMSKReplicator, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kafka replicators")
}

func scanKafkaVpcConnections(ctx context.Context, client kafkaExtAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := kafka.NewListVpcConnectionsPaginator(client, &kafka.ListVpcConnectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "kafka:ListVpcConnections", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("kafka:ListVpcConnections: %w", perr)
		}
		for _, v := range out.VpcConnections {
			arn := sv(v.VpcConnectionArn)
			if arn == "" {
				continue
			}
			label := sv(v.VpcId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMSKVpcConnection, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kafka vpc-connections")
}

func kafkaClusterARNs(clusters []kafkaCluster) []string {
	arns := make([]string, 0, len(clusters))
	for _, c := range clusters {
		arns = append(arns, c.arn)
	}
	return arns
}

// scanKafkaClusterPolicies stores each cluster's resource policy. Its model
// lists NotFoundException, which is skipped per cluster as no policy: whether
// it means the cluster has no policy, was deleted since listing, or both, is
// not verified. Otherwise a cluster's error is handled per the kafkaChildErrs
// policy: a denial, a transient error or a rejection skips only that cluster,
// with one warning for the region.
func scanKafkaClusterPolicies(ctx context.Context, client kafkaExtAPI, acct *account, region string, st *store.Store, scanID string, clusters []kafkaCluster) (int, int, error) {
	errs := &kafkaChildErrs{op: "kafka:GetClusterPolicy", gone: isKafkaGone}
	batch, err := describeFanOut(ctx, st, acct, region, errs.op, kafkaClusterARNs(clusters), errs.gone,
		func(ctx context.Context, clusterARN string) (*store.Resource, error) {
			out, err := client.GetClusterPolicy(ctx, &kafka.GetClusterPolicyInput{ClusterArn: &clusterARN})
			if err != nil && errs.tallied(clusterARN, err) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			if sv(out.Policy) == "" {
				return nil, nil
			}
			arn := clusterARN + "/cluster-policy"
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMSKClusterPolicy, NativeID: arn,
				Name: &arn, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
			}, nil
		})
	errs.warn(st, acct, region)
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, batch, "kafka cluster-policy")
}

// scanKafkaScramSecrets stores each cluster's SCRAM secret associations. Its
// model lists NotFoundException (a cluster deleted since listing), which is
// skipped per cluster; otherwise a cluster's error is handled per the
// kafkaChildErrs policy, as in scanKafkaClusterPolicies. childFanOut does not
// fit: it would link the rows under the cluster in the contains closure, which
// these rows have never had (the resolver links them by attached-to). The
// fan-out keeps its error policy through fanOutErrs instead.
func scanKafkaScramSecrets(ctx context.Context, client kafkaExtAPI, acct *account, region string, st *store.Store, scanID string, clusters []kafkaCluster) (int, int, error) {
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	errs := &kafkaChildErrs{op: "kafka:ListScramSecrets", gone: isKafkaGone}
	keyErrs := &fanOutErrs{st: st, acct: acct, region: region, op: errs.op, isSkip: errs.gone}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, c := range clusters {
		g.Go(func() error {
			rows, err := listKafkaScramSecrets(gctx, client, acct, region, scanID, c.arn)
			if err != nil && errs.tallied(c.arn, err) {
				return nil
			}
			if err != nil {
				return keyErrs.keyErr(c.arn, err)
			}
			mu.Lock()
			defer mu.Unlock()
			batch = append(batch, rows...)
			return nil
		})
	}
	err := g.Wait()
	errs.warn(st, acct, region)
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, batch, "kafka batch-scram-secrets")
}

// listKafkaScramSecrets builds one cluster's SCRAM secret rows.
func listKafkaScramSecrets(ctx context.Context, client kafkaExtAPI, acct *account, region, scanID, clusterARN string) ([]*store.Resource, error) {
	pager := kafka.NewListScramSecretsPaginator(client, &kafka.ListScramSecretsInput{ClusterArn: &clusterARN})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, secretARN := range out.SecretArnList {
			if secretARN == "" {
				continue
			}
			arn := fmt.Sprintf("%s/batch-scram-secret/%s", clusterARN, secretARN)
			label := secretARN
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMSKBatchScramSecret, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"ClusterArn": clusterARN, "SecretArn": secretARN}), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
