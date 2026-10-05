package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/mgn"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// Application Migration Service (MGN) — migration inventory: the migration
// pipeline (source servers, waves, templates) and the post-launch actions
// configured on source servers and launch configuration templates. Actions are
// contained by their source server or template; nothing carries outbound edges
// to other scanned AWS resource types. Action parameters are SSM Parameter
// Store names and dynamic paths, never values, so no Redact rule applies.
func init() {
	registerType(restype.Descriptor{Type: TypeMGNSourceServer, Service: "mgn"})
	registerType(restype.Descriptor{Type: TypeMGNApplication, Service: "mgn"})
	registerType(restype.Descriptor{Type: TypeMGNWave, Service: "mgn"})
	registerType(restype.Descriptor{Type: TypeMGNConnector, Service: "mgn"})
	registerType(restype.Descriptor{Type: TypeMGNLaunchConfigurationTemplate, Service: "mgn"})
	registerType(restype.Descriptor{Type: TypeMGNReplicationConfigurationTemplate, Service: "mgn"})
	registerType(restype.Descriptor{Type: TypeMGNVcenterClient, Service: "mgn"})
	registerType(restype.Descriptor{Type: TypeMGNNetworkMigrationDefinition, Service: "mgn"})
	registerType(restype.Descriptor{Type: TypeMGNSourceServerAction, Service: "mgn"})
	registerType(restype.Descriptor{Type: TypeMGNTemplateAction, Service: "mgn"})
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
	ListSourceServerActions(context.Context, *mgn.ListSourceServerActionsInput, ...func(*mgn.Options)) (*mgn.ListSourceServerActionsOutput, error)
	ListTemplateActions(context.Context, *mgn.ListTemplateActionsInput, ...func(*mgn.Options)) (*mgn.ListTemplateActionsOutput, error)
}

func scanMGN(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return scanMGNWithClient(ctx, mgn.NewFromConfig(acct.cfg, func(o *mgn.Options) { o.Region = region }), acct, region, st, scanID)
}

// scanMGNWithClient runs the action phases last, so a failing action list
// cannot keep any pre-existing phase from running.
func scanMGNWithClient(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var servers, templates []childParent
	type phase func(context.Context, mgnAPI, *account, string, *store.Store, string) (int, int, error)
	bind := func(p phase) func() (int, int, error) {
		return func() (int, int, error) { return p(ctx, client, acct, region, st, scanID) }
	}
	for _, p := range []func() (int, int, error){
		func() (t, i int, err error) {
			t, i, servers, err = scanMGNSourceServers(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		bind(scanMGNApplications),
		bind(scanMGNWaves),
		bind(scanMGNConnectors),
		func() (t, i int, err error) {
			t, i, templates, err = scanMGNLaunchConfigurationTemplates(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		bind(scanMGNReplicationConfigurationTemplates),
		bind(scanMGNVcenterClients),
		bind(scanMGNNetworkMigrationDefinitions),
		func() (int, int, error) {
			return childFanOut(ctx, st, acct, region, "mgn:ListSourceServerActions", servers, isMGNNotFound,
				func(ctx context.Context, p childParent) ([]*store.Resource, error) {
					return listMGNSourceServerActions(ctx, client, acct, region, scanID, p)
				})
		},
		func() (int, int, error) {
			return childFanOut(ctx, st, acct, region, "mgn:ListTemplateActions", templates, isMGNNotFound,
				func(ctx context.Context, p childParent) ([]*store.Resource, error) {
					return listMGNTemplateActions(ctx, client, acct, region, scanID, p)
				})
		},
	} {
		t, i, ferr := p()
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

// scanMGNSourceServers also returns the stored servers for the action phase.
func scanMGNSourceServers(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var batch []*store.Resource
	var parents []childParent
	p := mgn.NewDescribeSourceServersPaginator(client, &mgn.DescribeSourceServersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mgn:DescribeSourceServers", acct.ID, region, err)
				break
			}
			return 0, 0, nil, fmt.Errorf("mgn:DescribeSourceServers: %w", err)
		}
		for _, s := range page.Items {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			if id := sv(s.SourceServerID); id != "" {
				parents = append(parents, childParent{id: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNSourceServer, NativeID: arn, Region: &region,
				AttributesJSON: mustJSON(s), TagsJSON: mapTagsJSON(s.Tags), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "mgn source-servers")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
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

// scanMGNLaunchConfigurationTemplates also returns the stored templates for the
// action phase.
func scanMGNLaunchConfigurationTemplates(ctx context.Context, client mgnAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var batch []*store.Resource
	var parents []childParent
	p := mgn.NewDescribeLaunchConfigurationTemplatesPaginator(client, &mgn.DescribeLaunchConfigurationTemplatesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "mgn:DescribeLaunchConfigurationTemplates", acct.ID, region, err)
				break
			}
			return 0, 0, nil, fmt.Errorf("mgn:DescribeLaunchConfigurationTemplates: %w", err)
		}
		for _, t := range page.Items {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			if id := sv(t.LaunchConfigurationTemplateID); id != "" {
				parents = append(parents, childParent{id: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNLaunchConfigurationTemplate, NativeID: arn, Region: &region,
				AttributesJSON: mustJSON(t), TagsJSON: mapTagsJSON(t.Tags), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "mgn launch-configuration-templates")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
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

// isMGNNotFound matches a source server or launch configuration template
// deleted between its list call and its action list call.
func isMGNNotFound(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// Actions carry no ARN and the Service Reference defines no action resource,
// so the NativeID is {parentARN}/action/{actionID}.
func listMGNSourceServerActions(ctx context.Context, client mgnAPI, acct *account, region, scanID string, server childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	p := mgn.NewListSourceServerActionsPaginator(client, &mgn.ListSourceServerActionsInput{SourceServerID: &server.id})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range page.Items {
			id := sv(a.ActionID)
			if id == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNSourceServerAction, NativeID: server.arn + "/action/" + id, Name: a.ActionName, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}

func listMGNTemplateActions(ctx context.Context, client mgnAPI, acct *account, region, scanID string, template childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	p := mgn.NewListTemplateActionsPaginator(client, &mgn.ListTemplateActionsInput{LaunchConfigurationTemplateID: &template.id})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range page.Items {
			id := sv(a.ActionID)
			if id == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMGNTemplateAction, NativeID: template.arn + "/action/" + id, Name: a.ActionName, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
