package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/kafkaconnect"
)

func init() {
	registerService(serviceEntry{
		name: "aws:kafka-connect",
		fn:   scanKafkaConnect,
		emits: []coverage.TypeDecl{
			{Service: "kafka-connect", DiscoType: TypeKafkaConnectConnector},
			{Service: "kafka-connect", DiscoType: TypeKafkaConnectCustomPlugin, Leaf: true},
			{Service: "kafka-connect", DiscoType: TypeKafkaConnectWorkerConfiguration, Leaf: true},
		},
	})
}

type kafkaConnectAPI interface {
	ListConnectors(context.Context, *kafkaconnect.ListConnectorsInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.ListConnectorsOutput, error)
	ListCustomPlugins(context.Context, *kafkaconnect.ListCustomPluginsInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.ListCustomPluginsOutput, error)
	ListWorkerConfigurations(context.Context, *kafkaconnect.ListWorkerConfigurationsInput, ...func(*kafkaconnect.Options)) (*kafkaconnect.ListWorkerConfigurationsOutput, error)
}

// scanKafkaConnect discovers KafkaConnect connectors, custom plugins, and
// worker configurations. ARNs native on every type.
func scanKafkaConnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kafkaconnect.NewFromConfig(acct.cfg, func(o *kafkaconnect.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanKCConnectors(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanKCCustomPlugins(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanKCWorkerConfigurations(ctx, client, acct, region, st, scanID) },
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

func scanKCConnectors(ctx context.Context, client kafkaConnectAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := kafkaconnect.NewListConnectorsPaginator(client, &kafkaconnect.ListConnectorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kafkaconnect:ListConnectors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kafkaconnect:ListConnectors: %w", err)
		}
		for _, c := range out.Connectors {
			arn := sv(c.ConnectorArn)
			if arn == "" {
				continue
			}
			status := string(c.ConnectorState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKafkaConnectConnector, NativeID: arn,
				Name: c.ConnectorName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kafkaconnect connectors")
}

func scanKCCustomPlugins(ctx context.Context, client kafkaConnectAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := kafkaconnect.NewListCustomPluginsPaginator(client, &kafkaconnect.ListCustomPluginsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kafkaconnect:ListCustomPlugins", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kafkaconnect:ListCustomPlugins: %w", err)
		}
		for _, p := range out.CustomPlugins {
			arn := sv(p.CustomPluginArn)
			if arn == "" {
				continue
			}
			status := string(p.CustomPluginState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKafkaConnectCustomPlugin, NativeID: arn,
				Name: p.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kafkaconnect custom-plugins")
}

func scanKCWorkerConfigurations(ctx context.Context, client kafkaConnectAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := kafkaconnect.NewListWorkerConfigurationsPaginator(client, &kafkaconnect.ListWorkerConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kafkaconnect:ListWorkerConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kafkaconnect:ListWorkerConfigurations: %w", err)
		}
		for _, w := range out.WorkerConfigurations {
			arn := sv(w.WorkerConfigurationArn)
			if arn == "" {
				continue
			}
			status := string(w.WorkerConfigurationState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKafkaConnectWorkerConfiguration, NativeID: arn,
				Name: w.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kafkaconnect worker-configurations")
}
