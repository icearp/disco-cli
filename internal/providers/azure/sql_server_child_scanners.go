package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSQLElasticPool, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLFailoverGroup, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLJobAgent, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLRestorableDroppedDB, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLEncryptionProtector, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLServerAdministrator, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLServerKey, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLServerAdvancedThreatProt, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLServerSecurityAlert, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLServerVulnAssessment, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLServerAuditingSettings, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLServerExtAuditingSettings, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLServerDevOpsAuditSettings, Service: "microsoft.sql", Uncatalogued: true})
	registerType(restype.Descriptor{Type: TypeSQLServerDNSAlias, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLSyncAgent, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLVirtualNetworkRule, Service: "microsoft.sql"})
}

// sqlChildExtract is each child scanner's extractor output: id, name, plus optional
// tracked-resource fields (region, tags) — pointer/map so proxy scanners (no
// Location/Tags) can leave them nil.
type sqlChildExtract struct {
	id, name string
	region   *string
	tags     map[string]*string
}

// sqlChildScan is the shared body for SQL-server sub-resource scans: pages the
// ListByServer pager, maps each item to a Resource under its parent server, and
// upserts via sqlUpsert. AccessDenied/FeatureNotAvailable errors break the loop silently.
func sqlChildScan[C any, T any](
	ctx context.Context,
	label, rtype string,
	sub *subscription,
	st *store.Store,
	scanID string,
	srv sqlServer,
	pager azPager[C],
	pageItems func(C) []*T,
	extract func(*T) sqlChildExtract,
) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:%s.ListByServer(%s): %w", label, srv.name, err)
		}
		for _, item := range pageItems(page) {
			if item == nil {
				continue
			}
			e := extract(item)
			if e.id == "" {
				continue
			}
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           rtype,
				NativeID:       e.id,
				Name:           &e.name,
				Region:         e.region,
				TagsJSON:       azTagsJSON(e.tags),
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, e.id)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, label)
}

// sqlProxyExtract returns id+name for SQL proxy sub-resources with no
// per-resource Location or Tags (the majority of server child types).
func sqlProxyExtract(id, name *string) sqlChildExtract {
	return sqlChildExtract{id: sv(id), name: sv(name)}
}

// sqlTrackedExtract returns id+name+region+tags for SQL child types whose
// SDK shape is a tracked resource (ElasticPool, FailoverGroup, JobAgent,
// RestorableDroppedDB).
func sqlTrackedExtract(id, name, location *string, tags map[string]*string) sqlChildExtract {
	loc := sv(location)
	return sqlChildExtract{id: sv(id), name: sv(name), region: &loc, tags: tags}
}

// serverChildScanners returns one closure per server sub-resource type (excluding databases).
func serverChildScanners(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) []func() (int, int, error) {
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

func scanServerKeys(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewServerKeysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerKeysClient: %w", err)
	}
	return sqlChildScan(ctx, "ServerKeys", TypeSQLServerKey, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.ServerKeysClientListByServerResponse) []*armsql.ServerKey { return p.Value },
		func(x *armsql.ServerKey) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanEncryptionProtectors(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewEncryptionProtectorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewEncryptionProtectorsClient: %w", err)
	}
	return sqlChildScan(ctx, "EncryptionProtectors", TypeSQLEncryptionProtector, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.EncryptionProtectorsClientListByServerResponse) []*armsql.EncryptionProtector {
			return p.Value
		},
		func(x *armsql.EncryptionProtector) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanServerAdministrators(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewServerAzureADAdministratorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerAzureADAdministratorsClient: %w", err)
	}
	return sqlChildScan(ctx, "ServerAdministrators", TypeSQLServerAdministrator, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.ServerAzureADAdministratorsClientListByServerResponse) []*armsql.ServerAzureADAdministrator {
			return p.Value
		},
		func(x *armsql.ServerAzureADAdministrator) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanServerAuditingSettings(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewServerBlobAuditingPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerBlobAuditingPoliciesClient: %w", err)
	}
	return sqlChildScan(ctx, "ServerBlobAuditingPolicies", TypeSQLServerAuditingSettings, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.ServerBlobAuditingPoliciesClientListByServerResponse) []*armsql.ServerBlobAuditingPolicy {
			return p.Value
		},
		func(x *armsql.ServerBlobAuditingPolicy) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanServerExtAuditingSettings(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewExtendedServerBlobAuditingPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewExtendedServerBlobAuditingPoliciesClient: %w", err)
	}
	return sqlChildScan(ctx, "ExtendedServerBlobAuditingPolicies", TypeSQLServerExtAuditingSettings, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.ExtendedServerBlobAuditingPoliciesClientListByServerResponse) []*armsql.ExtendedServerBlobAuditingPolicy {
			return p.Value
		},
		func(x *armsql.ExtendedServerBlobAuditingPolicy) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanServerDevOpsAuditSettings(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewServerDevOpsAuditSettingsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerDevOpsAuditSettingsClient: %w", err)
	}
	return sqlChildScan(ctx, "ServerDevOpsAuditSettings", TypeSQLServerDevOpsAuditSettings, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.ServerDevOpsAuditSettingsClientListByServerResponse) []*armsql.ServerDevOpsAuditingSettings {
			return p.Value
		},
		func(x *armsql.ServerDevOpsAuditingSettings) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanServerSecurityAlertPolicies(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewServerSecurityAlertPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerSecurityAlertPoliciesClient: %w", err)
	}
	return sqlChildScan(ctx, "ServerSecurityAlertPolicies", TypeSQLServerSecurityAlert, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.ServerSecurityAlertPoliciesClientListByServerResponse) []*armsql.ServerSecurityAlertPolicy {
			return p.Value
		},
		func(x *armsql.ServerSecurityAlertPolicy) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanServerAdvancedThreatProtection(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewServerAdvancedThreatProtectionSettingsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerAdvancedThreatProtectionSettingsClient: %w", err)
	}
	return sqlChildScan(ctx, "ServerAdvancedThreatProtection", TypeSQLServerAdvancedThreatProt, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.ServerAdvancedThreatProtectionSettingsClientListByServerResponse) []*armsql.ServerAdvancedThreatProtection {
			return p.Value
		},
		func(x *armsql.ServerAdvancedThreatProtection) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanServerVulnAssessments(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewServerVulnerabilityAssessmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerVulnerabilityAssessmentsClient: %w", err)
	}
	return sqlChildScan(ctx, "ServerVulnerabilityAssessments", TypeSQLServerVulnAssessment, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.ServerVulnerabilityAssessmentsClientListByServerResponse) []*armsql.ServerVulnerabilityAssessment {
			return p.Value
		},
		func(x *armsql.ServerVulnerabilityAssessment) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanElasticPools(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewElasticPoolsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewElasticPoolsClient: %w", err)
	}
	return sqlChildScan(ctx, "ElasticPools", TypeSQLElasticPool, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.ElasticPoolsClientListByServerResponse) []*armsql.ElasticPool { return p.Value },
		func(x *armsql.ElasticPool) sqlChildExtract {
			return sqlTrackedExtract(x.ID, x.Name, x.Location, x.Tags)
		})
}

func scanFailoverGroups(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewFailoverGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewFailoverGroupsClient: %w", err)
	}
	return sqlChildScan(ctx, "FailoverGroups", TypeSQLFailoverGroup, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.FailoverGroupsClientListByServerResponse) []*armsql.FailoverGroup { return p.Value },
		func(x *armsql.FailoverGroup) sqlChildExtract {
			return sqlTrackedExtract(x.ID, x.Name, x.Location, x.Tags)
		})
}

func scanServerDNSAliases(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewServerDNSAliasesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServerDNSAliasesClient: %w", err)
	}
	return sqlChildScan(ctx, "ServerDNSAliases", TypeSQLServerDNSAlias, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.ServerDNSAliasesClientListByServerResponse) []*armsql.ServerDNSAlias { return p.Value },
		func(x *armsql.ServerDNSAlias) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanVirtualNetworkRules(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewVirtualNetworkRulesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewVirtualNetworkRulesClient: %w", err)
	}
	return sqlChildScan(ctx, "VirtualNetworkRules", TypeSQLVirtualNetworkRule, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.VirtualNetworkRulesClientListByServerResponse) []*armsql.VirtualNetworkRule {
			return p.Value
		},
		func(x *armsql.VirtualNetworkRule) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanJobAgents(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewJobAgentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewJobAgentsClient: %w", err)
	}
	return sqlChildScan(ctx, "JobAgents", TypeSQLJobAgent, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.JobAgentsClientListByServerResponse) []*armsql.JobAgent { return p.Value },
		func(x *armsql.JobAgent) sqlChildExtract { return sqlTrackedExtract(x.ID, x.Name, x.Location, x.Tags) })
}

func scanSyncAgents(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewSyncAgentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewSyncAgentsClient: %w", err)
	}
	return sqlChildScan(ctx, "SyncAgents", TypeSQLSyncAgent, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.SyncAgentsClientListByServerResponse) []*armsql.SyncAgent { return p.Value },
		func(x *armsql.SyncAgent) sqlChildExtract { return sqlProxyExtract(x.ID, x.Name) })
}

func scanRestorableDroppedDBs(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (int, int, error) {
	c, err := armsql.NewRestorableDroppedDatabasesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewRestorableDroppedDatabasesClient: %w", err)
	}
	return sqlChildScan(ctx, "RestorableDroppedDatabases", TypeSQLRestorableDroppedDB, sub, st, scanID, srv,
		c.NewListByServerPager(srv.rgName, srv.name, nil),
		func(p armsql.RestorableDroppedDatabasesClientListByServerResponse) []*armsql.RestorableDroppedDatabase {
			return p.Value
		},
		func(x *armsql.RestorableDroppedDatabase) sqlChildExtract {
			return sqlTrackedExtract(x.ID, x.Name, x.Location, x.Tags)
		})
}
