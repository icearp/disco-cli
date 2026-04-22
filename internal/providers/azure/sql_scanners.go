package azure

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// SQL type constants are defined in types.go.

func init() { registerService(serviceEntry{name: "azure:sql", fn: scanSQL}) }

// sqlServer holds the fields we need after listing servers.
type sqlServer struct {
	resourceID string // disco resource ID
	name       string
	rgName     string
}

// sqlDatabase holds the fields we need for per-database sub-resource fan-outs.
type sqlDatabase struct {
	resourceID string
	name       string
	serverName string
	rgName     string
}

// scanSQL discovers Azure SQL servers and their databases plus all supported sub-resources.
// Servers, instance pools, virtual clusters, and managed instances are scanned in parallel.
func scanSQL(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		mu     sync.Mutex
		total_ int
		ins_   int
	)
	add := func(t, i int) {
		mu.Lock()
		total_ += t
		ins_ += i
		mu.Unlock()
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		t, i, err := scanSQLServersAndChildren(gctx, sub, cred, st, scanID)
		add(t, i)
		return err
	})
	g.Go(func() error {
		t, i, err := scanSQLInstancePools(gctx, sub, cred, st, scanID)
		add(t, i)
		return err
	})
	g.Go(func() error {
		t, i, err := scanSQLVirtualClusters(gctx, sub, cred, st, scanID)
		add(t, i)
		return err
	})
	g.Go(func() error {
		t, i, err := scanSQLManaged(gctx, sub, cred, st, scanID)
		add(t, i)
		return err
	})
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return total_, ins_, nil
}

// scanSQLServersAndChildren lists servers, then fans out concurrently to databases
// and all 16 server-level sub-resources per server, then fans out to all 10
// database-level sub-resources per database.
func scanSQLServersAndChildren(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	serversClient, err := armsql.NewServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewServersClient: %w", err)
	}

	// Phase 1: list all servers.
	var servers []sqlServer
	pager := serversClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "armsql:Servers.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armsql:Servers.List: %w", err)
		}
		var batch []*store.Resource
		for _, srv := range page.Value {
			if srv.ID == nil || srv.Name == nil {
				continue
			}
			name := sv(srv.Name)
			location := sv(srv.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLServer,
				NativeID:       sv(srv.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(srv),
				DiscoveredBy:   scanID,
			}
			if srv.Tags != nil {
				s := mustJSON(srv.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			servers = append(servers, sqlServer{
				resourceID: store.ResourceID("azure", sub.ID, TypeSQLServer, sv(srv.ID)),
				name:       sv(srv.Name),
				rgName:     rgFromID(sv(srv.ID)),
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert SQL servers: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}

	if len(servers) == 0 {
		return total, inserted, nil
	}

	// Phase 2: per-server fan-out — databases + 16 server sub-resource types.
	// Semaphore bounds total concurrent goroutines across all servers.
	var (
		mu     sync.Mutex
		allDBs []sqlDatabase
	)

	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)

	for _, srv := range servers {
		// Databases — also yields sqlDatabase entries for phase 3.
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			t, i, dbs, err := scanDatabases(gctx, sub, cred, st, scanID, srv)
			mu.Lock()
			total += t
			inserted += i
			allDBs = append(allDBs, dbs...)
			mu.Unlock()
			return err
		})

		// Server sub-resources (16 types).
		for _, fn := range serverChildScanners(gctx, sub, cred, st, scanID, srv) {
			g.Go(func() error {
				if err := sem.Acquire(gctx, 1); err != nil {
					return err
				}
				defer sem.Release(1)
				t, i, err := fn()
				mu.Lock()
				total += t
				inserted += i
				mu.Unlock()
				return err
			})
		}
	}
	if err := g.Wait(); err != nil {
		return total, inserted, err
	}

	if len(allDBs) == 0 {
		return total, inserted, nil
	}

	// Phase 3: per-database fan-out — 10 database sub-resource types.
	g2, g2ctx := errgroup.WithContext(ctx)
	sem2 := semaphore.NewWeighted(maxConcurrentFanout)

	for _, db := range allDBs {
		for _, fn := range dbChildScanners(g2ctx, sub, cred, st, scanID, db) {
			g2.Go(func() error {
				if err := sem2.Acquire(g2ctx, 1); err != nil {
					return err
				}
				defer sem2.Release(1)
				t, i, err := fn()
				mu.Lock()
				total += t
				inserted += i
				mu.Unlock()
				return err
			})
		}
	}
	return total, inserted, g2.Wait()
}

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

// dbChildScanners returns one closure per database sub-resource type.
func dbChildScanners(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) []func() (int, int, error) {
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

// scanDatabases lists databases for a server, upserts them, and returns sqlDatabase
// entries for the database sub-resource fan-out in phase 3.
func scanDatabases(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, dbs []sqlDatabase, err error) {
	client, err := armsql.NewDatabasesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("armsql:NewDatabasesClient: %w", err)
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
			return 0, 0, nil, fmt.Errorf("armsql:Databases.ListByServer(%s): %w", srv.name, err)
		}
		for _, db := range page.Value {
			if db.ID == nil {
				continue
			}
			name := sv(db.Name)
			location := sv(db.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLDatabase,
				NativeID:       sv(db.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(db),
				DiscoveredBy:   scanID,
			}
			if db.Tags != nil {
				s := mustJSON(db.Tags)
				r.TagsJSON = &s
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLDatabase, sv(db.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, srv.resourceID})
			dbs = append(dbs, sqlDatabase{
				resourceID: discoID,
				name:       sv(db.Name),
				serverName: srv.name,
				rgName:     srv.rgName,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("upsert SQL databases: %w", err)
		}
		total += len(batch)
		inserted += n
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return 0, 0, nil, fmt.Errorf("closure SQL databases: %w", err)
		}
	}
	return total, inserted, dbs, nil
}

// — server sub-resource scanners —

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

// — database sub-resource scanners —

func scanDBTransparentDataEnc(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
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
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
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

func scanDBSecurityAlertPolicies(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
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
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
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

func scanDBAdvancedThreatProtection(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
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
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
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

func scanDBAuditingSettings(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
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
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
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

func scanDBVulnAssessments(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
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
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
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

func scanSyncGroups(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
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
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
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

func scanReplicationLinks(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
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
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
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

func scanWorkloadGroups(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
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
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
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

func scanGeoBackupPolicies(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
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
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
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

func scanLedgerDigestUploads(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string, db sqlDatabase) (total, inserted int, err error) {
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
			if isAccessDenied(err) || isFeatureNotAvailable(err) {
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

// — subscription-wide scanners —

func scanSQLInstancePools(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armsql.NewInstancePoolsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewInstancePoolsClient: %w", err)
	}
	pager := client.NewListPager(nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "armsql:InstancePools.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armsql:InstancePools.List: %w", err)
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
				Type:           TypeSQLInstancePool,
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
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeSQLInstancePool, sv(item.ID)))
		}
	}
	return sqlUpsert(st, batch, pairs, "instance pools")
}

func scanSQLVirtualClusters(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armsql.NewVirtualClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewVirtualClustersClient: %w", err)
	}
	pager := client.NewListPager(nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "armsql:VirtualClusters.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armsql:VirtualClusters.List: %w", err)
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
				Type:           TypeSQLVirtualCluster,
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
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeSQLVirtualCluster, sv(item.ID)))
		}
	}
	return sqlUpsert(st, batch, pairs, "virtual clusters")
}

// sqlUpsert upserts a batch of resources + hierarchy closure pairs.
// Returns total resources seen and newly inserted count.
func sqlUpsert(st *store.Store, batch []*store.Resource, pairs [][2]string, label string) (total, inserted int, err error) {
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert %s: %w", label, err)
	}
	if len(pairs) > 0 {
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure %s: %w", label, err)
		}
	}
	return len(batch), n, nil
}
