package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
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

func scanKafkaClusterPolicy(ctx context.Context, client kafkaExtAPI, acct *account, region string, st *store.Store, scanID, clusterARN string) (int, int, error) {
	carn := clusterARN
	out, err := client.GetClusterPolicy(ctx, &kafka.GetClusterPolicyInput{ClusterArn: &carn})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "NotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("kafka:GetClusterPolicy: %w", err)
	}
	if out == nil || sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("%s/cluster-policy", clusterARN)
	label := arn
	batch := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeMSKClusterPolicy, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}}
	return upsertBatch(st, batch, "kafka cluster-policy")
}

func scanKafkaScramSecrets(ctx context.Context, client kafkaExtAPI, acct *account, region string, st *store.Store, scanID, clusterARN string) (int, int, error) {
	carn := clusterARN
	pager := kafka.NewListScramSecretsPaginator(client, &kafka.ListScramSecretsInput{ClusterArn: &carn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("kafka:ListScramSecrets: %w", perr)
		}
		for _, secretARN := range out.SecretArnList {
			if secretARN == "" {
				continue
			}
			arn := fmt.Sprintf("%s/batch-scram-secret/%s", clusterARN, secretARN)
			label := secretARN
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMSKBatchScramSecret, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"ClusterArn": clusterARN, "SecretArn": secretARN}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kafka batch-scram-secrets")
}
