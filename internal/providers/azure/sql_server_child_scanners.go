package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

// serverChildScanners returns one closure per server sub-resource type (excluding databases).
func serverChildScanners(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) []func() (int, int, error) {
	return []func() (int, int, error){
		func() (int, int, error) { return scanServerKeys(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanEncryptionProtectors(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanServerAdministrators(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanServerAuditingSettings(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanServerExtAuditingSettings(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanServerDevOpsAuditSettings(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanServerSecurityAlertPolicies(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanServerAdvancedThreatProtection(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanServerVulnAssessments(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanElasticPools(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanFailoverGroups(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanServerDNSAliases(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanVirtualNetworkRules(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanJobAgents(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanSyncAgents(ctx, sub, cred, st, scanID, srv) },
		func() (int, int, error) { return scanRestorableDroppedDBs(ctx, sub, cred, st, scanID, srv) },
	}
}

func scanServerKeys(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewServerKeysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerKeysClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ServerKeys.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServerKey,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLServerKey, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "server keys")
}

func scanEncryptionProtectors(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewEncryptionProtectorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewEncryptionProtectorsClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:EncryptionProtectors.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLEncryptionProtector,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLEncryptionProtector, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "encryption protectors")
}

func scanServerAdministrators(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewServerAzureADAdministratorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerAzureADAdministratorsClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ServerAdministrators.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServerAdministrator,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLServerAdministrator, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "server administrators")
}

func scanServerAuditingSettings(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewServerBlobAuditingPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerBlobAuditingPoliciesClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ServerBlobAuditingPolicies.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServerAuditingSettings,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLServerAuditingSettings, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "server auditing settings")
}

func scanServerExtAuditingSettings(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewExtendedServerBlobAuditingPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewExtendedServerBlobAuditingPoliciesClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ExtendedServerBlobAuditingPolicies.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServerExtAuditingSettings,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLServerExtAuditingSettings, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "server extended auditing settings")
}

func scanServerDevOpsAuditSettings(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewServerDevOpsAuditSettingsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerDevOpsAuditSettingsClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ServerDevOpsAuditSettings.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServerDevOpsAuditSettings,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLServerDevOpsAuditSettings, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "server devops audit settings")
}

func scanServerSecurityAlertPolicies(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewServerSecurityAlertPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerSecurityAlertPoliciesClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ServerSecurityAlertPolicies.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServerSecurityAlert,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLServerSecurityAlert, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "server security alert policies")
}

func scanServerAdvancedThreatProtection(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewServerAdvancedThreatProtectionSettingsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerAdvancedThreatProtectionSettingsClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ServerAdvancedThreatProtection.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServerAdvancedThreatProt,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLServerAdvancedThreatProt, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "server advanced threat protection settings")
}

func scanServerVulnAssessments(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewServerVulnerabilityAssessmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerVulnerabilityAssessmentsClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ServerVulnerabilityAssessments.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServerVulnAssessment,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLServerVulnAssessment, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "server vulnerability assessments")
}

func scanElasticPools(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewElasticPoolsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewElasticPoolsClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ElasticPools.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			location := sv(item.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLElasticPool,
				NativeID:       sv(item.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			if item.Tags != nil {
				s := mustJSON(item.Tags)
				r.TagsJSON = &s
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLElasticPool, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "elastic pools")
}

func scanFailoverGroups(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewFailoverGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewFailoverGroupsClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:FailoverGroups.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			location := sv(item.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLFailoverGroup,
				NativeID:       sv(item.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			if item.Tags != nil {
				s := mustJSON(item.Tags)
				r.TagsJSON = &s
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLFailoverGroup, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "failover groups")
}

func scanServerDNSAliases(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewServerDNSAliasesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerDNSAliasesClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ServerDNSAliases.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServerDNSAlias,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLServerDNSAlias, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "server DNS aliases")
}

func scanVirtualNetworkRules(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewVirtualNetworkRulesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewVirtualNetworkRulesClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:VirtualNetworkRules.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLVirtualNetworkRule,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLVirtualNetworkRule, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "virtual network rules")
}

func scanJobAgents(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewJobAgentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewJobAgentsClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:JobAgents.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			location := sv(item.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLJobAgent,
				NativeID:       sv(item.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			if item.Tags != nil {
				s := mustJSON(item.Tags)
				r.TagsJSON = &s
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLJobAgent, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "job agents")
}

func scanSyncAgents(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewSyncAgentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewSyncAgentsClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:SyncAgents.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLSyncAgent,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLSyncAgent, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "sync agents")
}

func scanRestorableDroppedDBs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, err error) {
	client, err := armsql.NewRestorableDroppedDatabasesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewRestorableDroppedDatabasesClient: %w", err)
	}
	pager := client.NewListByServerPager(srv.rgName, srv.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:RestorableDroppedDatabases.ListByServer(%s): %w", srv.name, err)
		}
		for _, item := range page.Value {
			if item.ID == nil {
				continue
			}
			name := sv(item.Name)
			location := sv(item.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLRestorableDroppedDB,
				NativeID:       sv(item.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLRestorableDroppedDB, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "restorable dropped databases")
}
