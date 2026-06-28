package azure

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	// Uncatalogued flags mark proxy child types disco scans that ARM
	// Providers/List never enumerates as standalone resourceTypes (see
	// azure/CLAUDE.md) — real resources, but absent from the upstream registry.
	registerExtraEmits(
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedInstance},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedDatabase},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedDatabaseSecAlert, Uncatalogued: true},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedDatabaseTDE, Uncatalogued: true},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedDatabaseVA},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedInstanceAdmin},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedInstanceEP, Uncatalogued: true},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedInstanceKey, Uncatalogued: true},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedInstancePEC, Uncatalogued: true},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedInstanceVA},
		coverage.TypeDecl{Service: "microsoft.sql", DiscoType: TypeSQLManagedServerSecurityAlert, Uncatalogued: true},
	)
}

// sqlManagedInstance holds the fields we need after listing managed instances.
type sqlManagedInstance struct {
	resourceID string // disco resource ID
	name       string
	rgName     string
}

// sqlManagedDatabase holds the fields we need for per-MI-database sub-resource fan-outs.
type sqlManagedDatabase struct {
	resourceID string
	name       string
	miName     string
	rgName     string
}

// scanSQLManaged discovers managed instances and their databases, administrators,
// vulnerability assessments, and managed database vulnerability assessments.
// Called concurrently from scanSQL alongside the server-based scanners.
func scanSQLManaged(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	miClient, err := armsql.NewManagedInstancesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewManagedInstancesClient: %w", err)
	}

	// Phase 1: list all managed instances.
	var instances []sqlManagedInstance
	pager := miClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armsql:ManagedInstances.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armsql:ManagedInstances.List: %w", err)
		}
		var batch []*store.Resource
		for _, mi := range page.Value {
			if mi.ID == nil || mi.Name == nil {
				continue
			}
			name := sv(mi.Name)
			location := sv(mi.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeSQLManagedInstance,
				NativeID:       sv(mi.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(mi),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(mi.Tags)
			batch = append(batch, r)
			instances = append(instances, sqlManagedInstance{
				resourceID: store.ResourceID("azure", sub.ID, TypeSQLManagedInstance, sv(mi.ID)),
				name:       sv(mi.Name),
				rgName:     rgFromID(sv(mi.ID)),
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert managed instances: %w", err)
			}
			total += len(batch)
			inserted += n
			var pairs [][2]string
			for _, b := range batch {
				pairs = append(pairs, rgHierarchyPair(sub, TypeSQLManagedInstance, b.NativeID))
			}
			if err := st.RecordHierarchyBatch(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure managed instances: %w", err)
			}
		}
	}

	if len(instances) == 0 {
		return total, inserted, nil
	}

	// Phase 2: per-MI fan-out — databases + administrators + vulnerability assessments.
	var (
		mu       sync.Mutex
		allMIDBs []sqlManagedDatabase
	)

	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)

	for _, mi := range instances {
		// Managed databases — also yields sqlManagedDatabase entries for phase 3.
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			t, i, dbs, err := scanManagedDatabases(gctx, sub, cred, st, scanID, mi)
			mu.Lock()
			total += t
			inserted += i
			allMIDBs = append(allMIDBs, dbs...)
			mu.Unlock()
			return err
		})

		// MI administrators.
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			t, i, err := scanManagedInstanceAdmins(gctx, sub, cred, st, scanID, mi)
			mu.Lock()
			total += t
			inserted += i
			mu.Unlock()
			return err
		})

		// MI vulnerability assessments.
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			t, i, err := scanManagedInstanceVulnAssessments(gctx, sub, cred, st, scanID, mi)
			mu.Lock()
			total += t
			inserted += i
			mu.Unlock()
			return err
		})

		// MI keys, encryption protectors, private endpoint connections,
		// and managed-server security alert policies — each its own goroutine
		// so slow lists don't stall siblings.
		for _, fn := range managedInstanceChildScanners(gctx, sub, cred, st, scanID, mi) {
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

	if len(allMIDBs) == 0 {
		return total, inserted, nil
	}

	// Phase 3: per-MI-database fan-out — managed database vulnerability assessments.
	g2, g2ctx := errgroup.WithContext(ctx)
	sem2 := semaphore.NewWeighted(maxConcurrentFanout)

	for _, db := range allMIDBs {
		for _, fn := range managedDatabaseChildScanners(g2ctx, sub, cred, st, scanID, db) {
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
	// Wait BEFORE reading the counters — Go evaluates return-list expressions
	// left-to-right, so inlining g2.Wait() at position 3 reads total/inserted
	// while phase-3 goroutines are still running.
	err = g2.Wait()
	return total, inserted, err
}

// managedInstanceChildScanners returns closures for MI-level sub-resource scanners
// that run in the phase-2 fan-out (alongside databases, admins, VAs).
func managedInstanceChildScanners(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, mi sqlManagedInstance) []func() (int, int, error) {
	return []func() (int, int, error){
		func() (int, int, error) { return scanManagedInstanceKeys(ctx, sub, cred, st, scanID, mi) },
		func() (int, int, error) {
			return scanManagedInstanceEncryptionProtectors(ctx, sub, cred, st, scanID, mi)
		},
		func() (int, int, error) {
			return scanManagedInstancePrivateEndpointConnections(ctx, sub, cred, st, scanID, mi)
		},
		func() (int, int, error) {
			return scanManagedServerSecurityAlertPolicies(ctx, sub, cred, st, scanID, mi)
		},
	}
}

// managedDatabaseChildScanners returns closures for MDB-level sub-resource scanners
// that run in the phase-3 fan-out (alongside MDB VAs).
func managedDatabaseChildScanners(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlManagedDatabase) []func() (int, int, error) {
	return []func() (int, int, error){
		func() (int, int, error) { return scanManagedDatabaseVulnAssessments(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) { return scanManagedDatabaseTDE(ctx, sub, cred, st, scanID, db) },
		func() (int, int, error) {
			return scanManagedDatabaseSecurityAlertPolicies(ctx, sub, cred, st, scanID, db)
		},
	}
}

func scanManagedInstanceKeys(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, mi sqlManagedInstance) (total, inserted int, err error) {
	client, err := armsql.NewManagedInstanceKeysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewManagedInstanceKeysClient: %w", err)
	}
	pager := client.NewListByInstancePager(mi.rgName, mi.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ManagedInstanceKeys.ListByInstance(%s): %w", mi.name, err)
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
				Type:           TypeSQLManagedInstanceKey,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLManagedInstanceKey, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, mi.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "managed instance keys")
}

func scanManagedInstanceEncryptionProtectors(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, mi sqlManagedInstance) (total, inserted int, err error) {
	client, err := armsql.NewManagedInstanceEncryptionProtectorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewManagedInstanceEncryptionProtectorsClient: %w", err)
	}
	pager := client.NewListByInstancePager(mi.rgName, mi.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ManagedInstanceEncryptionProtectors.ListByInstance(%s): %w", mi.name, err)
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
				Type:           TypeSQLManagedInstanceEP,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLManagedInstanceEP, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, mi.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "managed instance encryption protectors")
}

func scanManagedInstancePrivateEndpointConnections(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, mi sqlManagedInstance) (total, inserted int, err error) {
	client, err := armsql.NewManagedInstancePrivateEndpointConnectionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewManagedInstancePrivateEndpointConnectionsClient: %w", err)
	}
	pager := client.NewListByManagedInstancePager(mi.rgName, mi.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ManagedInstancePrivateEndpointConnections.ListByManagedInstance(%s): %w", mi.name, err)
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
				Type:           TypeSQLManagedInstancePEC,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLManagedInstancePEC, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, mi.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "managed instance private endpoint connections")
}

func scanManagedServerSecurityAlertPolicies(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, mi sqlManagedInstance) (total, inserted int, err error) {
	client, err := armsql.NewManagedServerSecurityAlertPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewManagedServerSecurityAlertPoliciesClient: %w", err)
	}
	pager := client.NewListByInstancePager(mi.rgName, mi.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ManagedServerSecurityAlertPolicies.ListByInstance(%s): %w", mi.name, err)
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
				Type:           TypeSQLManagedServerSecurityAlert,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLManagedServerSecurityAlert, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, mi.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "managed server security alert policies")
}

func scanManagedDatabaseTDE(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlManagedDatabase) (total, inserted int, err error) {
	client, err := armsql.NewManagedDatabaseTransparentDataEncryptionClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewManagedDatabaseTransparentDataEncryptionClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.miName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ManagedDatabaseTransparentDataEncryption.ListByDatabase(%s/%s): %w", db.miName, db.name, err)
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
				Type:           TypeSQLManagedDatabaseTDE,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLManagedDatabaseTDE, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "managed database transparent data encryptions")
}

func scanManagedDatabaseSecurityAlertPolicies(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlManagedDatabase) (total, inserted int, err error) {
	client, err := armsql.NewManagedDatabaseSecurityAlertPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewManagedDatabaseSecurityAlertPoliciesClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.miName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ManagedDatabaseSecurityAlertPolicies.ListByDatabase(%s/%s): %w", db.miName, db.name, err)
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
				Type:           TypeSQLManagedDatabaseSecAlert,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLManagedDatabaseSecAlert, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "managed database security alert policies")
}

// scanManagedDatabases lists databases for a managed instance, upserts them, and
// returns sqlManagedDatabase entries for phase 3 sub-resource fan-out.
func scanManagedDatabases(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, mi sqlManagedInstance) (total, inserted int, dbs []sqlManagedDatabase, err error) {
	client, err := armsql.NewManagedDatabasesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("armsql:NewManagedDatabasesClient: %w", err)
	}
	pager := client.NewListByInstancePager(mi.rgName, mi.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, nil, fmt.Errorf("armsql:ManagedDatabases.ListByInstance(%s): %w", mi.name, err)
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
				Type:           TypeSQLManagedDatabase,
				NativeID:       sv(db.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(db),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLManagedDatabase, sv(db.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, mi.resourceID})
			dbs = append(dbs, sqlManagedDatabase{
				resourceID: discoID,
				name:       sv(db.Name),
				miName:     mi.name,
				rgName:     mi.rgName,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("upsert managed databases: %w", err)
		}
		total += len(batch)
		inserted += n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, nil, fmt.Errorf("closure managed databases: %w", err)
		}
	}
	return total, inserted, dbs, nil
}

func scanManagedInstanceAdmins(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, mi sqlManagedInstance) (total, inserted int, err error) {
	client, err := armsql.NewManagedInstanceAdministratorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewManagedInstanceAdministratorsClient: %w", err)
	}
	pager := client.NewListByInstancePager(mi.rgName, mi.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ManagedInstanceAdministrators.ListByInstance(%s): %w", mi.name, err)
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
				Type:           TypeSQLManagedInstanceAdmin,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLManagedInstanceAdmin, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, mi.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "managed instance administrators")
}

func scanManagedInstanceVulnAssessments(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, mi sqlManagedInstance) (total, inserted int, err error) {
	client, err := armsql.NewManagedInstanceVulnerabilityAssessmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewManagedInstanceVulnerabilityAssessmentsClient: %w", err)
	}
	pager := client.NewListByInstancePager(mi.rgName, mi.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ManagedInstanceVulnerabilityAssessments.ListByInstance(%s): %w", mi.name, err)
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
				Type:           TypeSQLManagedInstanceVA,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLManagedInstanceVA, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, mi.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "managed instance vulnerability assessments")
}

func scanManagedDatabaseVulnAssessments(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, db sqlManagedDatabase) (total, inserted int, err error) {
	client, err := armsql.NewManagedDatabaseVulnerabilityAssessmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsql:NewManagedDatabaseVulnerabilityAssessmentsClient: %w", err)
	}
	pager := client.NewListByDatabasePager(db.rgName, db.miName, db.name, nil)
	var batch []*store.Resource
	var pairs [][2]string
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
				break
			}
			return 0, 0, fmt.Errorf("armsql:ManagedDatabaseVulnerabilityAssessments.ListByDatabase(%s/%s): %w", db.miName, db.name, err)
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
				Type:           TypeSQLManagedDatabaseVA,
				NativeID:       sv(item.ID),
				Name:           &name,
				AttributesJSON: mustJSON(item),
				DiscoveredBy:   scanID,
			}
			discoID := store.ResourceID("azure", sub.ID, TypeSQLManagedDatabaseVA, sv(item.ID))
			batch = append(batch, r)
			pairs = append(pairs, [2]string{discoID, db.resourceID})
		}
	}
	return sqlUpsert(st, batch, pairs, "managed database vulnerability assessments")
}
