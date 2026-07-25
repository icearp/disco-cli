package azure

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// SQL type constants: types.go.
// Server sub-resource scanners: sql_server_child_scanners.go.
// Database sub-resource scanners: sql_database_child_scanners.go.
// Managed instance + managed database scanners: sql_managed_scanners.go.

func init() {
	registerType(restype.Descriptor{Type: TypeSQLServer, Service: "microsoft.sql", Redact: []redact.Rule{{Path: "properties.administratorLoginPassword", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeSQLDatabase, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLVirtualCluster, Service: "microsoft.sql"})
	registerType(restype.Descriptor{Type: TypeSQLInstancePool, Service: "microsoft.sql"})
	registerService(serviceEntry{
		name: "azure:microsoft.sql",
		fn:   scanSQL,
	})
}

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

// scanSQL discovers Azure SQL servers, their databases, and all supported sub-resources.
// Servers, instance pools, virtual clusters, and managed instances scan in parallel.
func scanSQL(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	// Top-level phases run concurrently via azRunPhases (no cross-phase cancellation) — a
	// failure in one (e.g. managed instances) must not abort servers/pools/clusters
	// ("errors never abort scan"). Per-server/per-database fan-out inside
	// scanSQLServersAndChildren keeps its own bounded errgroup, matching azRGFanoutScan.
	return azRunPhases(
		func() (int, int, error) { return scanSQLServersAndChildren(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanSQLInstancePools(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanSQLVirtualClusters(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanSQLManaged(ctx, sub, cred, st, scanID) },
	)
}

// scanSQLServersAndChildren lists servers, fans out concurrently to databases and all
// 16 server-level sub-resources per server, then fans out to all 10 database-level
// sub-resources per database.
func scanSQLServersAndChildren(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
			if isSkippableScanError(err) {
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
			r.TagsJSON = azTagsJSON(srv.Tags)
			batch = append(batch, r)
			servers = append(servers, sqlServer{
				resourceID: store.ResourceID("azure", sub.ID, sv(srv.ID)),
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
	// Semaphore bounds total concurrent goroutines across servers.
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
	// Wait BEFORE reading counters — Go evaluates return-list expressions
	// left-to-right; inlining g2.Wait() at position 3 would read total/inserted
	// while phase-3 goroutines are still running.
	err = g2.Wait()
	return total, inserted, err
}

// scanDatabases lists databases for a server, upserts them, and returns sqlDatabase
// entries for the database sub-resource fan-out in phase 3.
func scanDatabases(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, srv sqlServer) (total, inserted int, dbs []sqlDatabase, err error) {
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
			if isSkippableScanError(err) || isFeatureNotAvailable(err) {
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
			r.TagsJSON = azTagsJSON(db.Tags)
			discoID := store.ResourceID("azure", sub.ID, sv(db.ID))
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
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, nil, fmt.Errorf("closure SQL databases: %w", err)
		}
	}
	return total, inserted, dbs, nil
}

// — subscription-wide scanners —

func scanSQLInstancePools(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
			if isSkippableScanError(err) {
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
			r.TagsJSON = azTagsJSON(item.Tags)
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeSQLInstancePool, sv(item.ID)))
		}
	}
	return sqlUpsert(st, batch, pairs, "instance pools")
}

func scanSQLVirtualClusters(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
			if isSkippableScanError(err) {
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
			r.TagsJSON = azTagsJSON(item.Tags)
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
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure %s: %w", label, err)
		}
	}
	return len(batch), n, nil
}
