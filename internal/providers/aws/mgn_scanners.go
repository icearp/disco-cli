package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/mgn"
)

// Application Migration Service (MGN) — migration inventory. All resources are
// leaf: they describe the migration pipeline (source servers, waves, templates)
// and carry no outbound edges to other scanned AWS resource types.
func init() {
	registerType(restype.Descriptor{Type: TypeMGNSourceServer, Service: "mgn", Upstream: "AWS::mgn::SourceServerResource", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMGNApplication, Service: "mgn", Upstream: "AWS::mgn::ApplicationResource", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMGNWave, Service: "mgn", Upstream: "AWS::mgn::WaveResource", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMGNConnector, Service: "mgn", Upstream: "AWS::mgn::ConnectorResource", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMGNLaunchConfigurationTemplate, Service: "mgn", Upstream: "AWS::mgn::LaunchConfigurationTemplateResource", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMGNReplicationConfigurationTemplate, Service: "mgn", Upstream: "AWS::mgn::ReplicationConfigurationTemplateResource", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMGNVcenterClient, Service: "mgn", Upstream: "AWS::mgn::VcenterClientResource", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMGNNetworkMigrationDefinition, Service: "mgn", Upstream: "AWS::mgn::NetworkMigrationDefinitionResource", Leaf: true})
	registerService(serviceEntry{
		name: "aws:mgn",
		fn:   scanMGN,
	})
}

type mgnAPI interface {
	DescribeSourceServers(context.Context, *mgn.DescribeSourceServersInput, ...func(*mgn.Options)) (*mgn.DescribeSourceServersOutput, error)
	ListApplications(context.Context, *mgn.ListApplicationsInput, ...func(*mgn.Options)) (*mgn.ListApplicationsOutput, error)
	ListWaves(context.Context, *mgn.ListWavesInput, ...func(*mgn.Options)) (*mgn.ListWavesOutput, error)
	ListConnectors(context.Context, *mgn.ListConnectorsInput, ...func(*mgn.Options)) (*mgn.ListConnectorsOutput, error)
	DescribeLaunchConfigurationTemplates(context.Context, *mgn.DescribeLaunchConfigurationTemplatesInput, ...func(*mgn.Options)) (*mgn.DescribeLaunchConfigurationTemplatesOutput, error)
	DescribeReplicationConfigurationTemplates(context.Context, *mgn.DescribeReplicationConfigurationTemplatesInput, ...func(*mgn.Options)) (*mgn.DescribeReplicationConfigurationTemplatesOutput, error)
	DescribeVcenterClients(context.Context, *mgn.DescribeVcenterClientsInput, ...func(*mgn.Options)) (*mgn.DescribeVcenterClientsOutput, error)
	ListNetworkMigrationDefinitions(context.Context, *mgn.ListNetworkMigrationDefinitionsInput, ...func(*mgn.Options)) (*mgn.ListNetworkMigrationDefinitionsOutput, error)
}

func scanMGN(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mgn.NewFromConfig(acct.cfg, func(o *mgn.Options) { o.Region = region })
	type phase func(context.Context, mgnAPI, *account, string, *store.Store, string) (int, int, error)
	for _, p := range []phase{
		scanMGNSourceServers,
		scanMGNApplications,
		scanMGNWaves,
		scanMGNConnectors,
		scanMGNLaunchConfigurationTemplates,
		scanMGNReplicationConfigurationTemplates,
		scanMGNVcenterClients,
		scanMGNNetworkMigrationDefinitions,
	} {
		t, i, ferr := p(ctx, client, acct, region, st, scanID)
		total += t
		inserted += i
		if ferr != nil {
			if isAccountNotInitialized(ferr) {
				return 0, 0, markServiceDisabled(ferr)
			}
			return total, inserted, ferr
		}
	}
	return total, inserted, nil
}

func scanMGNSourceServers(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := mgn.NewDescribeSourceServersPaginator(client, &mgn.DescribeSourceServersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mgn:DescribeSourceServers", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("mgn:DescribeSourceServers: %w", err)
		}
		for _, s := range page.Items {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNSourceServer, NativeID: arn, Region: &region,
				AttributesJSON: mustJSON(s), TagsJSON: mapTagsJSON(s.Tags), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mgn source-servers")
}

func scanMGNApplications(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := mgn.NewListApplicationsPaginator(client, &mgn.ListApplicationsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mgn:ListApplications", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("mgn:ListApplications: %w", err)
		}
		for _, a := range page.Items {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNApplication, NativeID: arn, Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), TagsJSON: mapTagsJSON(a.Tags), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mgn applications")
}

func scanMGNWaves(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := mgn.NewListWavesPaginator(client, &mgn.ListWavesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mgn:ListWaves", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("mgn:ListWaves: %w", err)
		}
		for _, w := range page.Items {
			arn := sv(w.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNWave, NativeID: arn, Name: w.Name, Region: &region,
				AttributesJSON: mustJSON(w), TagsJSON: mapTagsJSON(w.Tags), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mgn waves")
}

func scanMGNConnectors(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := mgn.NewListConnectorsPaginator(client, &mgn.ListConnectorsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mgn:ListConnectors", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("mgn:ListConnectors: %w", err)
		}
		for _, c := range page.Items {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNConnector, NativeID: arn, Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), TagsJSON: mapTagsJSON(c.Tags), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mgn connectors")
}

func scanMGNLaunchConfigurationTemplates(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := mgn.NewDescribeLaunchConfigurationTemplatesPaginator(client, &mgn.DescribeLaunchConfigurationTemplatesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mgn:DescribeLaunchConfigurationTemplates", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("mgn:DescribeLaunchConfigurationTemplates: %w", err)
		}
		for _, t := range page.Items {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNLaunchConfigurationTemplate, NativeID: arn, Region: &region,
				AttributesJSON: mustJSON(t), TagsJSON: mapTagsJSON(t.Tags), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mgn launch-configuration-templates")
}

func scanMGNReplicationConfigurationTemplates(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := mgn.NewDescribeReplicationConfigurationTemplatesPaginator(client, &mgn.DescribeReplicationConfigurationTemplatesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mgn:DescribeReplicationConfigurationTemplates", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("mgn:DescribeReplicationConfigurationTemplates: %w", err)
		}
		for _, t := range page.Items {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNReplicationConfigurationTemplate, NativeID: arn, Region: &region,
				AttributesJSON: mustJSON(t), TagsJSON: mapTagsJSON(t.Tags), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mgn replication-configuration-templates")
}

func scanMGNVcenterClients(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := mgn.NewDescribeVcenterClientsPaginator(client, &mgn.DescribeVcenterClientsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mgn:DescribeVcenterClients", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("mgn:DescribeVcenterClients: %w", err)
		}
		for _, v := range page.Items {
			arn := sv(v.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNVcenterClient, NativeID: arn, Region: &region,
				AttributesJSON: mustJSON(v), TagsJSON: mapTagsJSON(v.Tags), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mgn vcenter-clients")
}

func scanMGNNetworkMigrationDefinitions(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	p := mgn.NewListNetworkMigrationDefinitionsPaginator(client, &mgn.ListNetworkMigrationDefinitionsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mgn:ListNetworkMigrationDefinitions", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("mgn:ListNetworkMigrationDefinitions: %w", err)
		}
		for _, d := range page.Items {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNNetworkMigrationDefinition, NativeID: arn, Name: d.Name, Region: &region,
				AttributesJSON: mustJSON(d), TagsJSON: mapTagsJSON(d.Tags), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mgn network-migration-definitions")
}
