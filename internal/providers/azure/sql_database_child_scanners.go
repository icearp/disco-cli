package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLDBTransparentDataEnc},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLDBVulnAssessment},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLDBSecurityAlert},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLDBAdvancedThreatProt},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLDBAuditingSettings},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLGeoBackupPolicy},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLLedgerDigestUpload},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLReplicationLink},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLSyncGroup},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLWorkloadGroup},
	)
}

// dbChildScanners returns one closure per database sub-resource type.
func dbChildScanners(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) []func() (int, int, error) {
	return []func() (int, int, error){
		func() (int, int, error) { return scanDBTransparentDataEnc(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) { return scanDBSecurityAlertPolicies(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) { return scanDBAdvancedThreatProtection(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) { return scanDBAuditingSettings(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) { return scanDBVulnAssessments(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) { return scanSyncGroups(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) { return scanReplicationLinks(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) { return scanWorkloadGroups(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) { return scanGeoBackupPolicies(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) { return scanLedgerDigestUploads(ctx, sub, cred, st, scanID, db) },
	}
}

func scanDBTransparentDataEnc(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
	client, err := armsql.NewTransparentDataEncryptionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewTransparentDataEncryptionsClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.serverName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:TransparentDataEncryptions.ListByDatabase(%s/%s): %w", db.serverName, db.name, err)
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
				Type:           TypeSQLDBTransparentDataEnc,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLDBTransparentDataEnc, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "database transparent data encryptions")
}

func scanDBSecurityAlertPolicies(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
	client, err := armsql.NewDatabaseSecurityAlertPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewDatabaseSecurityAlertPoliciesClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.serverName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:DatabaseSecurityAlertPolicies.ListByDatabase(%s/%s): %w", db.serverName, db.name, err)
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
				Type:           TypeSQLDBSecurityAlert,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLDBSecurityAlert, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "database security alert policies")
}

func scanDBAdvancedThreatProtection(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
	client, err := armsql.NewDatabaseAdvancedThreatProtectionSettingsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewDatabaseAdvancedThreatProtectionSettingsClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.serverName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:DatabaseAdvancedThreatProtection.ListByDatabase(%s/%s): %w", db.serverName, db.name, err)
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
				Type:           TypeSQLDBAdvancedThreatProt,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLDBAdvancedThreatProt, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "database advanced threat protection settings")
}

func scanDBAuditingSettings(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
	client, err := armsql.NewDatabaseBlobAuditingPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewDatabaseBlobAuditingPoliciesClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.serverName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:DatabaseBlobAuditingPolicies.ListByDatabase(%s/%s): %w", db.serverName, db.name, err)
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
				Type:           TypeSQLDBAuditingSettings,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLDBAuditingSettings, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "database auditing settings")
}

func scanDBVulnAssessments(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
	client, err := armsql.NewDatabaseVulnerabilityAssessmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewDatabaseVulnerabilityAssessmentsClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.serverName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:DatabaseVulnerabilityAssessments.ListByDatabase(%s/%s): %w", db.serverName, db.name, err)
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
				Type:           TypeSQLDBVulnAssessment,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLDBVulnAssessment, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "database vulnerability assessments")
}

func scanSyncGroups(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
	client, err := armsql.NewSyncGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewSyncGroupsClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.serverName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:SyncGroups.ListByDatabase(%s/%s): %w", db.serverName, db.name, err)
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
				Type:           TypeSQLSyncGroup,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLSyncGroup, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "sync groups")
}

func scanReplicationLinks(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
	client, err := armsql.NewReplicationLinksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewReplicationLinksClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.serverName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ReplicationLinks.ListByDatabase(%s/%s): %w", db.serverName, db.name, err)
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
				Type:           TypeSQLReplicationLink,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLReplicationLink, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "replication links")
}

func scanWorkloadGroups(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
	client, err := armsql.NewWorkloadGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewWorkloadGroupsClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.serverName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:WorkloadGroups.ListByDatabase(%s/%s): %w", db.serverName, db.name, err)
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
				Type:           TypeSQLWorkloadGroup,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLWorkloadGroup, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "workload groups")
}

func scanGeoBackupPolicies(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
	client, err := armsql.NewGeoBackupPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewGeoBackupPoliciesClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.serverName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:GeoBackupPolicies.ListByDatabase(%s/%s): %w", db.serverName, db.name, err)
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
				Type:           TypeSQLGeoBackupPolicy,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLGeoBackupPolicy, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "geo backup policies")
}

func scanLedgerDigestUploads(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
	client, err := armsql.NewLedgerDigestUploadsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewLedgerDigestUploadsClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.serverName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:LedgerDigestUploads.ListByDatabase(%s/%s): %w", db.serverName, db.name, err)
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
				Type:           TypeSQLLedgerDigestUpload,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLLedgerDigestUpload, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "ledger digest uploads")
}
