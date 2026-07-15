package gcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/bigtableadmin/v2"
	"google.golang.org/api/firestore/v1"
	"google.golang.org/api/spanner/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBigtableInstance, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/Instance", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBigtableCluster, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/Cluster"})
	registerType(restype.Descriptor{Type: TypeBigtableBackup, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/Backup"})
	registerType(restype.Descriptor{Type: TypeBigtableAppProfile, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/AppProfile"})
	registerType(restype.Descriptor{Type: TypeBigtableTable, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/Table"})
	registerType(restype.Descriptor{Type: TypeBigtableAuthorizedView, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/AuthorizedView", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBigtableLogicalView, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/LogicalView", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBigtableMaterializedView, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/MaterializedView", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBigtableSchemaBundle, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/SchemaBundle", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBigtableHotTablet, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/HotTablet"})
	registerType(restype.Descriptor{Type: TypeBigtableMemoryLayer, Service: "bigtableadmin", Upstream: "bigtableadmin.googleapis.com/MemoryLayer", Leaf: true})
	registerType(restype.Descriptor{Type: TypeFirestoreDB, Service: "firestore", Upstream: "firestore.googleapis.com/Database"})
	registerType(restype.Descriptor{Type: TypeFirestoreBackup, Service: "firestore", Upstream: "firestore.googleapis.com/Backup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeFirestoreBackupSchedule, Service: "firestore", Upstream: "firestore.googleapis.com/BackupSchedule", Leaf: true})
	registerType(restype.Descriptor{Type: TypeFirestoreUserCred, Service: "firestore", Upstream: "firestore.googleapis.com/UserCred", Leaf: true, Redact: []redact.Rule{{Path: "securePassword", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeSpannerInstance, Service: "spanner", Upstream: "spanner.googleapis.com/Instance"})
	registerType(restype.Descriptor{Type: TypeSpannerDatabase, Service: "spanner", Upstream: "spanner.googleapis.com/Database"})
	registerType(restype.Descriptor{Type: TypeSpannerInstanceConfig, Service: "spanner", Upstream: "spanner.googleapis.com/InstanceConfig"})
	registerType(restype.Descriptor{Type: TypeSpannerInstancePartition, Service: "spanner", Upstream: "spanner.googleapis.com/InstancePartition"})
	registerType(restype.Descriptor{Type: TypeSpannerBackup, Service: "spanner", Upstream: "spanner.googleapis.com/Backup"})
	registerType(restype.Descriptor{Type: TypeSpannerBackupSchedule, Service: "spanner", Upstream: "spanner.googleapis.com/BackupSchedule"})
	registerType(restype.Descriptor{Type: TypeSpannerDatabaseRole, Service: "spanner", Upstream: "spanner.googleapis.com/DatabaseRole", Leaf: true})
	registerService(serviceEntry{
		name: "gcp:bigtable",
		fn:   scanBigtable,
	})
	registerService(serviceEntry{
		name: "gcp:firestore",
		fn:   scanFirestore,
	})
	registerService(serviceEntry{
		name: "gcp:spanner",
		fn:   scanSpanner,
	})
}

// maxConcurrentSpannerFanout caps per-Instance Backup and per-Database
// BackupSchedule/DatabaseRole fan-out. Per-project cardinality is low; keep
// modest like the DNS/logging/monitoring per-item fan-outs.
const maxConcurrentSpannerFanout = 10

// maxConcurrentBigtableFanout caps per-Instance/per-Cluster/per-Table fan-out
// (AppProfile, Table, LogicalView, MaterializedView, AuthorizedView,
// SchemaBundle, Backup, HotTablet, MemoryLayer). Per-project cardinality is
// modest; keep consistent with the other providers' per-item fan-outs.
const maxConcurrentBigtableFanout = 10

// maxConcurrentFirestoreFanout caps per-Database BackupSchedule/UserCred
// fan-out.
const maxConcurrentFirestoreFanout = 10

// scanBigtable discovers Bigtable instances, clusters, and their secondary
// resources: AppProfiles (project-wide wildcard, per-row Instance derived
// from name), Tables/LogicalViews/MaterializedViews (fan-out per Instance),
// AuthorizedViews/SchemaBundles (fan-out per Table), Backups/MemoryLayers
// (fan-out per Instance using the cluster wildcard, per-row Cluster derived
// from name), and HotTablets (fan-out per Cluster — no wildcard support).
func scanBigtable(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := bigtableadmin.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("bigtableadmin client: %w", err)
	}

	instances, t, n, err := scanBigtableInstances(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	clusters, t, n, err := scanBigtableClusters(ctx, svc, p, instances, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanBigtableAppProfiles(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	tableNames, t, n, err := scanBigtableTables(ctx, svc, p, instances, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanBigtableLogicalViews(ctx, svc, p, instances, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanBigtableMaterializedViews(ctx, svc, p, instances, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanBigtableAuthorizedViews(ctx, svc, p, tableNames, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanBigtableSchemaBundles(ctx, svc, p, tableNames, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanBigtableBackups(ctx, svc, p, instances, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanBigtableHotTablets(ctx, svc, p, clusters, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanBigtableMemoryLayers(ctx, svc, p, instances, st, scanID)
	total += t
	inserted += n
	return total, inserted, err
}

// scanBigtableInstances discovers Bigtable instances. NextPageToken on
// ListInstancesResponse is SDK-doc-marked "DEPRECATED: unused and ignored",
// so a single .Do() call (not .Pages()) is correct here — unlike Spanner's
// Databases.List, this endpoint genuinely never paginates.
func scanBigtableInstances(ctx context.Context, svc *bigtableadmin.Service, p *project, st *store.Store, scanID string) (instances []*bigtableadmin.Instance, total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	resp, err := svc.Projects.Instances.List(parent).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return nil, 0, 0, skipIfDenied(st, "bigtableadmin:instances.list", p.ID, err)
		}
		return nil, 0, 0, err
	}
	var batch []*store.Resource
	for _, inst := range resp.Instances {
		instances = append(instances, inst)
		name := lastSegment(inst.Name)
		batch = append(batch, &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           TypeBigtableInstance,
			NativeID:       inst.Name,
			Name:           &name,
			Status:         strp(inst.State),
			AttributesJSON: mustJSON(inst),
			DiscoveredBy:   scanID,
		})
	}
	total, inserted, err = upsertWithProjClosure(p, st, batch)
	return instances, total, inserted, err
}

// scanBigtableClusters discovers clusters per already-scanned Instance
// (same NextPageToken-deprecated reasoning as scanBigtableInstances — single
// .Do() call is correct). Returns the flat cluster list for the
// HotTablet per-Cluster fan-out.
func scanBigtableClusters(ctx context.Context, svc *bigtableadmin.Service, p *project, instances []*bigtableadmin.Instance, st *store.Store, scanID string) (clusters []*bigtableadmin.Cluster, total, inserted int, err error) {
	for _, inst := range instances {
		cresp, cerr := svc.Projects.Instances.Clusters.List(inst.Name).Context(ctx).Do()
		if cerr != nil {
			if isPermissionDenied(cerr) {
				_ = skipIfDenied(st, "bigtableadmin:clusters.list", p.ID, cerr)
				continue
			}
			return clusters, total, inserted, cerr
		}
		var cbatch []*store.Resource
		for _, c := range cresp.Clusters {
			clusters = append(clusters, c)
			cname := lastSegment(c.Name)
			cbatch = append(cbatch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeBigtableCluster,
				NativeID:       c.Name,
				Name:           &cname,
				Region:         strp(lastSegment(c.Location)),
				Status:         strp(c.State),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		instResID := store.ResourceID("gcp", p.ID, inst.Name)
		ct, cn, uerr := upsertWithParent(st, cbatch, instResID)
		total += ct
		inserted += cn
		if uerr != nil {
			return clusters, total, inserted, uerr
		}
	}
	return clusters, total, inserted, nil
}

// scanBigtableAppProfiles discovers app profiles across every instance via
// the project-wide wildcard parent (SDK doc confirms `{instance} = '-'`
// support). Each page may mix profiles from multiple instances, so the
// owning Instance is derived per-row by splitting the profile's own resource
// name (KMS-style / Spanner-InstancePartitions-style).
func scanBigtableAppProfiles(ctx context.Context, svc *bigtableadmin.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s/instances/-", p.ID)
	return runPaginated(ctx, st, p, "bigtableadmin:appProfiles.list",
		svc.Projects.Instances.AppProfiles.List(parent),
		func(page *bigtableadmin.ListAppProfilesResponse) (int, int, error) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, ap := range page.AppProfiles {
				if ap == nil || ap.Name == "" {
					continue
				}
				instanceNative, _, ok := strings.Cut(ap.Name, "/appProfiles/")
				if !ok {
					continue
				}
				name := lastSegment(ap.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBigtableAppProfile,
					NativeID:       ap.Name,
					Name:           &name,
					AttributesJSON: mustJSON(ap),
					DiscoveredBy:   scanID,
				})
				instID := store.ResourceID("gcp", p.ID, instanceNative)
				apID := store.ResourceID("gcp", p.ID, ap.Name)
				pairs = append(pairs, [2]string{apID, instID})
			}
			n, upErr := st.UpsertResources(batch)
			if upErr != nil {
				return 0, 0, fmt.Errorf("upsert bigtable app profiles: %w", upErr)
			}
			if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
				return 0, 0, fmt.Errorf("closure bigtable app profiles: %w", cErr)
			}
			return len(batch), n, nil
		})
}

// scanBigtableTables fans out Tables.List per already-scanned Instance (no
// wildcard parent support). Returns every table's NativeID (across all
// instances) for the AuthorizedView/SchemaBundle per-Table fan-out.
func scanBigtableTables(ctx context.Context, svc *bigtableadmin.Service, p *project, instances []*bigtableadmin.Instance, st *store.Store, scanID string) (tableNames []string, total, inserted int, err error) {
	var mu sync.Mutex
	if ferr := forEachItem(ctx, maxConcurrentBigtableFanout, instances, func(gctx context.Context, inst *bigtableadmin.Instance) error {
		if inst == nil || inst.Name == "" {
			return nil
		}
		instResID := store.ResourceID("gcp", p.ID, inst.Name)
		var batch []*store.Resource
		var names []string
		listErr := svc.Projects.Instances.Tables.List(inst.Name).Pages(gctx, func(page *bigtableadmin.ListTablesResponse) error {
			for _, tb := range page.Tables {
				if tb == nil || tb.Name == "" {
					continue
				}
				names = append(names, tb.Name)
				name := lastSegment(tb.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBigtableTable,
					NativeID:       tb.Name,
					Name:           &name,
					AttributesJSON: mustJSON(tb),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "bigtableadmin:tables.list", inst.Name, listErr)
			}
			return listErr
		}
		t, n, uerr := upsertWithParent(st, batch, instResID)
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		tableNames = append(tableNames, names...)
		return uerr
	}); ferr != nil {
		return tableNames, total, inserted, ferr
	}
	return tableNames, total, inserted, nil
}

// scanBigtableLogicalViews fans out LogicalViews.List per already-scanned
// Instance (no wildcard parent support).
func scanBigtableLogicalViews(ctx context.Context, svc *bigtableadmin.Service, p *project, instances []*bigtableadmin.Instance, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if ferr := forEachItem(ctx, maxConcurrentBigtableFanout, instances, func(gctx context.Context, inst *bigtableadmin.Instance) error {
		if inst == nil || inst.Name == "" {
			return nil
		}
		instResID := store.ResourceID("gcp", p.ID, inst.Name)
		var batch []*store.Resource
		listErr := svc.Projects.Instances.LogicalViews.List(inst.Name).Pages(gctx, func(page *bigtableadmin.ListLogicalViewsResponse) error {
			for _, lv := range page.LogicalViews {
				if lv == nil || lv.Name == "" {
					continue
				}
				name := lastSegment(lv.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBigtableLogicalView,
					NativeID:       lv.Name,
					Name:           &name,
					AttributesJSON: mustJSON(lv),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "bigtableadmin:logicalViews.list", inst.Name, listErr)
			}
			return listErr
		}
		t, n, uerr := upsertWithParent(st, batch, instResID)
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		return uerr
	}); ferr != nil {
		return total, inserted, ferr
	}
	return total, inserted, nil
}

// scanBigtableMaterializedViews fans out MaterializedViews.List per
// already-scanned Instance (no wildcard parent support).
func scanBigtableMaterializedViews(ctx context.Context, svc *bigtableadmin.Service, p *project, instances []*bigtableadmin.Instance, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if ferr := forEachItem(ctx, maxConcurrentBigtableFanout, instances, func(gctx context.Context, inst *bigtableadmin.Instance) error {
		if inst == nil || inst.Name == "" {
			return nil
		}
		instResID := store.ResourceID("gcp", p.ID, inst.Name)
		var batch []*store.Resource
		listErr := svc.Projects.Instances.MaterializedViews.List(inst.Name).Pages(gctx, func(page *bigtableadmin.ListMaterializedViewsResponse) error {
			for _, mv := range page.MaterializedViews {
				if mv == nil || mv.Name == "" {
					continue
				}
				name := lastSegment(mv.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBigtableMaterializedView,
					NativeID:       mv.Name,
					Name:           &name,
					AttributesJSON: mustJSON(mv),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "bigtableadmin:materializedViews.list", inst.Name, listErr)
			}
			return listErr
		}
		t, n, uerr := upsertWithParent(st, batch, instResID)
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		return uerr
	}); ferr != nil {
		return total, inserted, ferr
	}
	return total, inserted, nil
}

// scanBigtableAuthorizedViews fans out AuthorizedViews.List per
// already-scanned Table (no wildcard parent support).
func scanBigtableAuthorizedViews(ctx context.Context, svc *bigtableadmin.Service, p *project, tableNames []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if ferr := forEachItem(ctx, maxConcurrentBigtableFanout, tableNames, func(gctx context.Context, tableName string) error {
		tableResID := store.ResourceID("gcp", p.ID, tableName)
		var batch []*store.Resource
		listErr := svc.Projects.Instances.Tables.AuthorizedViews.List(tableName).Pages(gctx, func(page *bigtableadmin.ListAuthorizedViewsResponse) error {
			for _, av := range page.AuthorizedViews {
				if av == nil || av.Name == "" {
					continue
				}
				name := lastSegment(av.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBigtableAuthorizedView,
					NativeID:       av.Name,
					Name:           &name,
					AttributesJSON: mustJSON(av),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "bigtableadmin:authorizedViews.list", tableName, listErr)
			}
			return listErr
		}
		t, n, uerr := upsertWithParent(st, batch, tableResID)
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		return uerr
	}); ferr != nil {
		return total, inserted, ferr
	}
	return total, inserted, nil
}

// scanBigtableSchemaBundles fans out SchemaBundles.List per already-scanned
// Table (no wildcard parent support).
func scanBigtableSchemaBundles(ctx context.Context, svc *bigtableadmin.Service, p *project, tableNames []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if ferr := forEachItem(ctx, maxConcurrentBigtableFanout, tableNames, func(gctx context.Context, tableName string) error {
		tableResID := store.ResourceID("gcp", p.ID, tableName)
		var batch []*store.Resource
		listErr := svc.Projects.Instances.Tables.SchemaBundles.List(tableName).Pages(gctx, func(page *bigtableadmin.ListSchemaBundlesResponse) error {
			for _, sb := range page.SchemaBundles {
				if sb == nil || sb.Name == "" {
					continue
				}
				name := lastSegment(sb.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBigtableSchemaBundle,
					NativeID:       sb.Name,
					Name:           &name,
					AttributesJSON: mustJSON(sb),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "bigtableadmin:schemaBundles.list", tableName, listErr)
			}
			return listErr
		}
		t, n, uerr := upsertWithParent(st, batch, tableResID)
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		return uerr
	}); ferr != nil {
		return total, inserted, ferr
	}
	return total, inserted, nil
}

// scanBigtableBackups fans out Backups.List per already-scanned Instance,
// using the cluster wildcard (SDK doc confirms `{cluster} = '-'` support) so
// one call covers every cluster in the instance. Each page may mix backups
// from multiple clusters, so the owning Cluster is derived per-row by
// splitting the backup's own resource name.
func scanBigtableBackups(ctx context.Context, svc *bigtableadmin.Service, p *project, instances []*bigtableadmin.Instance, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if ferr := forEachItem(ctx, maxConcurrentBigtableFanout, instances, func(gctx context.Context, inst *bigtableadmin.Instance) error {
		if inst == nil || inst.Name == "" {
			return nil
		}
		parent := inst.Name + "/clusters/-"
		var batch []*store.Resource
		var pairs [][2]string
		listErr := svc.Projects.Instances.Clusters.Backups.List(parent).Pages(gctx, func(page *bigtableadmin.ListBackupsResponse) error {
			for _, b := range page.Backups {
				if b == nil || b.Name == "" {
					continue
				}
				clusterNative, _, ok := strings.Cut(b.Name, "/backups/")
				if !ok {
					continue
				}
				name := lastSegment(b.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBigtableBackup,
					NativeID:       b.Name,
					Name:           &name,
					Status:         strp(b.State),
					AttributesJSON: mustJSON(b),
					DiscoveredBy:   scanID,
				})
				clusterID := store.ResourceID("gcp", p.ID, clusterNative)
				backupID := store.ResourceID("gcp", p.ID, b.Name)
				pairs = append(pairs, [2]string{backupID, clusterID})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "bigtableadmin:backups.list", inst.Name, listErr)
			}
			return listErr
		}
		n, upErr := st.UpsertResources(batch)
		if upErr != nil {
			return fmt.Errorf("upsert bigtable backups: %w", upErr)
		}
		if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
			return fmt.Errorf("closure bigtable backups: %w", cErr)
		}
		mu.Lock()
		defer mu.Unlock()
		total += len(batch)
		inserted += n
		return nil
	}); ferr != nil {
		return total, inserted, ferr
	}
	return total, inserted, nil
}

// scanBigtableHotTablets fans out HotTablets.List per already-scanned
// Cluster — unlike Backups/MemoryLayers, this endpoint has no cluster
// wildcard, so per-Cluster fan-out (not per-Instance) is the only shape.
func scanBigtableHotTablets(ctx context.Context, svc *bigtableadmin.Service, p *project, clusters []*bigtableadmin.Cluster, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if ferr := forEachItem(ctx, maxConcurrentBigtableFanout, clusters, func(gctx context.Context, c *bigtableadmin.Cluster) error {
		if c == nil || c.Name == "" {
			return nil
		}
		clusterResID := store.ResourceID("gcp", p.ID, c.Name)
		var batch []*store.Resource
		listErr := svc.Projects.Instances.Clusters.HotTablets.List(c.Name).Pages(gctx, func(page *bigtableadmin.ListHotTabletsResponse) error {
			for _, ht := range page.HotTablets {
				if ht == nil || ht.Name == "" {
					continue
				}
				name := lastSegment(ht.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBigtableHotTablet,
					NativeID:       ht.Name,
					Name:           &name,
					AttributesJSON: mustJSON(ht),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "bigtableadmin:hotTablets.list", c.Name, listErr)
			}
			return listErr
		}
		t, n, uerr := upsertWithParent(st, batch, clusterResID)
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		return uerr
	}); ferr != nil {
		return total, inserted, ferr
	}
	return total, inserted, nil
}

// scanBigtableMemoryLayers fans out MemoryLayers.List per already-scanned
// Instance, using the cluster wildcard (SDK doc confirms `{cluster} = '-'`
// support). Each page may mix memory layers from multiple clusters; the
// owning Cluster is derived per-row by trimming the fixed `/memoryLayer`
// suffix from the resource name (there is always exactly one memory layer
// per cluster, unlike Backup's variable-cardinality child).
func scanBigtableMemoryLayers(ctx context.Context, svc *bigtableadmin.Service, p *project, instances []*bigtableadmin.Instance, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if ferr := forEachItem(ctx, maxConcurrentBigtableFanout, instances, func(gctx context.Context, inst *bigtableadmin.Instance) error {
		if inst == nil || inst.Name == "" {
			return nil
		}
		parent := inst.Name + "/clusters/-"
		var batch []*store.Resource
		var pairs [][2]string
		listErr := svc.Projects.Instances.Clusters.MemoryLayers.List(parent).Pages(gctx, func(page *bigtableadmin.ListMemoryLayersResponse) error {
			for _, ml := range page.MemoryLayers {
				if ml == nil || ml.Name == "" {
					continue
				}
				clusterNative := strings.TrimSuffix(ml.Name, "/memoryLayer")
				if clusterNative == ml.Name {
					continue
				}
				name := lastSegment(clusterNative) + "/memoryLayer"
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeBigtableMemoryLayer,
					NativeID:       ml.Name,
					Name:           &name,
					Status:         strp(ml.State),
					AttributesJSON: mustJSON(ml),
					DiscoveredBy:   scanID,
				})
				clusterID := store.ResourceID("gcp", p.ID, clusterNative)
				mlID := store.ResourceID("gcp", p.ID, ml.Name)
				pairs = append(pairs, [2]string{mlID, clusterID})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "bigtableadmin:memoryLayers.list", inst.Name, listErr)
			}
			return listErr
		}
		n, upErr := st.UpsertResources(batch)
		if upErr != nil {
			return fmt.Errorf("upsert bigtable memory layers: %w", upErr)
		}
		if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
			return fmt.Errorf("closure bigtable memory layers: %w", cErr)
		}
		mu.Lock()
		defer mu.Unlock()
		total += len(batch)
		inserted += n
		return nil
	}); ferr != nil {
		return total, inserted, ferr
	}
	return total, inserted, nil
}

// scanFirestore discovers Firestore databases (multi-DB project support),
// backups (project-wide wildcard, each row carries its owning Database's
// full resource name directly — no name-splitting needed), and
// BackupSchedules/UserCreds (fan-out per Database). Indexes / collection
// groups / fields deferred — narrow graph value vs. per-database fan-out
// cost.
func scanFirestore(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := firestore.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("firestore client: %w", err)
	}

	databaseNames, t, n, err := scanFirestoreDatabases(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanFirestoreBackups(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanFirestoreBackupSchedules(ctx, svc, p, databaseNames, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanFirestoreUserCreds(ctx, svc, p, databaseNames, st, scanID)
	total += t
	inserted += n
	return total, inserted, err
}

// scanFirestoreDatabases discovers Firestore databases. ListDatabasesResponse
// has no NextPageToken (no pagination on this endpoint). Returns every
// database's NativeID for the BackupSchedule/UserCred per-Database fan-out.
func scanFirestoreDatabases(ctx context.Context, svc *firestore.Service, p *project, st *store.Store, scanID string) (databaseNames []string, total, inserted int, err error) {
	resp, err := svc.Projects.Databases.List(fmt.Sprintf("projects/%s", p.ID)).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return nil, 0, 0, skipIfDenied(st, "firestore:databases.list", p.ID, err)
		}
		return nil, 0, 0, err
	}
	var batch []*store.Resource
	for _, d := range resp.Databases {
		if d == nil || d.Name == "" {
			continue
		}
		databaseNames = append(databaseNames, d.Name)
		name := lastSegment(d.Name)
		batch = append(batch, &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           TypeFirestoreDB,
			NativeID:       d.Name,
			Name:           &name,
			Region:         strp(d.LocationId),
			CreatedAt:      strp(d.CreateTime),
			AttributesJSON: mustJSON(d),
			DiscoveredBy:   scanID,
		})
	}
	total, inserted, err = upsertWithProjClosure(p, st, batch)
	return databaseNames, total, inserted, err
}

// scanFirestoreBackups discovers backups across every location via the
// project-wide wildcard parent (SDK doc confirms `{location} = '-'`
// support). ListBackupsResponse has no NextPageToken (no pagination). Unlike
// the Spanner/Bigtable multi-parent cases, each Backup carries its owning
// Database's full resource name directly in its own `Database` field — no
// name-splitting needed.
func scanFirestoreBackups(ctx context.Context, svc *firestore.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	resp, err := svc.Projects.Locations.Backups.List(parent).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "firestore:backups.list", p.ID, err)
		}
		return 0, 0, err
	}
	var batch []*store.Resource
	var pairs [][2]string
	for _, b := range resp.Backups {
		if b == nil || b.Name == "" || b.Database == "" {
			continue
		}
		name := lastSegment(b.Name)
		batch = append(batch, &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           TypeFirestoreBackup,
			NativeID:       b.Name,
			Name:           &name,
			Status:         strp(b.State),
			AttributesJSON: mustJSON(b),
			DiscoveredBy:   scanID,
		})
		dbID := store.ResourceID("gcp", p.ID, b.Database)
		backupID := store.ResourceID("gcp", p.ID, b.Name)
		pairs = append(pairs, [2]string{backupID, dbID})
	}
	n, upErr := st.UpsertResources(batch)
	if upErr != nil {
		return 0, 0, fmt.Errorf("upsert firestore backups: %w", upErr)
	}
	if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
		return 0, 0, fmt.Errorf("closure firestore backups: %w", cErr)
	}
	return len(batch), n, nil
}

// scanFirestoreBackupSchedules fans out BackupSchedules.List per
// already-scanned Database (no wildcard parent support).
// ListBackupSchedulesResponse has no NextPageToken (no pagination).
func scanFirestoreBackupSchedules(ctx context.Context, svc *firestore.Service, p *project, databaseNames []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if ferr := forEachItem(ctx, maxConcurrentFirestoreFanout, databaseNames, func(gctx context.Context, dbName string) error {
		dbResID := store.ResourceID("gcp", p.ID, dbName)
		resp, listErr := svc.Projects.Databases.BackupSchedules.List(dbName).Context(gctx).Do()
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "firestore:backupSchedules.list", dbName, listErr)
			}
			return listErr
		}
		var batch []*store.Resource
		for _, bs := range resp.BackupSchedules {
			if bs == nil || bs.Name == "" {
				continue
			}
			name := lastSegment(bs.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeFirestoreBackupSchedule,
				NativeID:       bs.Name,
				Name:           &name,
				AttributesJSON: mustJSON(bs),
				DiscoveredBy:   scanID,
			})
		}
		t, n, uerr := upsertWithParent(st, batch, dbResID)
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		return uerr
	}); ferr != nil {
		return total, inserted, ferr
	}
	return total, inserted, nil
}

// scanFirestoreUserCreds fans out UserCreds.List per already-scanned
// Database (no wildcard parent support). ListUserCredsResponse has no
// NextPageToken (no pagination). The SDK doc states List "does not contain
// the secret value itself" — SecurePassword is only ever populated on
// Create/ResetPassword responses — but redact.Register still flags the
// field defensively since the type is shared and this is a credential-
// shaped field.
func scanFirestoreUserCreds(ctx context.Context, svc *firestore.Service, p *project, databaseNames []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if ferr := forEachItem(ctx, maxConcurrentFirestoreFanout, databaseNames, func(gctx context.Context, dbName string) error {
		dbResID := store.ResourceID("gcp", p.ID, dbName)
		resp, listErr := svc.Projects.Databases.UserCreds.List(dbName).Context(gctx).Do()
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "firestore:userCreds.list", dbName, listErr)
			}
			return listErr
		}
		var batch []*store.Resource
		for _, uc := range resp.UserCreds {
			if uc == nil || uc.Name == "" {
				continue
			}
			name := lastSegment(uc.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeFirestoreUserCred,
				NativeID:       uc.Name,
				Name:           &name,
				Status:         strp(uc.State),
				CreatedAt:      strp(uc.CreateTime),
				AttributesJSON: mustJSON(uc),
				DiscoveredBy:   scanID,
			})
		}
		t, n, uerr := upsertWithParent(st, batch, dbResID)
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		return uerr
	}); ferr != nil {
		return total, inserted, ferr
	}
	return total, inserted, nil
}

// scanSpanner discovers Spanner instances, databases, instance configs,
// instance partitions, backups (fan-out per Instance), and backup schedules
// + database roles (fan-out per Database). Sessions deferred — runtime
// objects, not durable configuration.
func scanSpanner(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := spanner.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("spanner client: %w", err)
	}

	instances, t, n, err := scanSpannerInstances(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanSpannerInstanceConfigs(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanSpannerInstancePartitions(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	databaseNames, t, n, err := scanSpannerDatabases(ctx, svc, p, instances, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanSpannerBackups(ctx, svc, p, instances, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanSpannerBackupSchedules(ctx, svc, p, databaseNames, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanSpannerDatabaseRoles(ctx, svc, p, databaseNames, st, scanID)
	total += t
	inserted += n
	return total, inserted, err
}

// scanSpannerInstances discovers Spanner instances. Returns the instances
// for phase's per-Instance Database/Backup fan-out.
func scanSpannerInstances(ctx context.Context, svc *spanner.Service, p *project, st *store.Store, scanID string) (instances []*spanner.Instance, total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	total, inserted, err = runPaginated(ctx, st, p, "spanner:instances.list",
		svc.Projects.Instances.List(parent),
		func(page *spanner.ListInstancesResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Instances))
			for _, inst := range page.Instances {
				instances = append(instances, inst)
				name := lastSegment(inst.Name)
				region := lastSegment(inst.Config) // "projects/{p}/instanceConfigs/{region-or-multi}"
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeSpannerInstance,
					NativeID:       inst.Name,
					Name:           &name,
					Region:         strp(region),
					Status:         strp(inst.State),
					AttributesJSON: mustJSON(inst),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	return instances, total, inserted, err
}

// scanSpannerInstanceConfigs discovers instance configurations — a mix of
// Google-managed catalog entries and user-defined custom configs, both
// listed by the same call (no separate filter to split them).
func scanSpannerInstanceConfigs(ctx context.Context, svc *spanner.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	return runPaginated(ctx, st, p, "spanner:instanceConfigs.list",
		svc.Projects.InstanceConfigs.List(parent),
		func(page *spanner.ListInstanceConfigsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.InstanceConfigs))
			for _, ic := range page.InstanceConfigs {
				if ic == nil || ic.Name == "" {
					continue
				}
				name := ic.DisplayName
				batch = append(batch, &store.Resource{
					Provider:          "gcp",
					AccountID:         p.ID,
					AccountName:       &p.Name,
					Type:              TypeSpannerInstanceConfig,
					NativeID:          ic.Name,
					Name:              &name,
					Status:            strp(ic.State),
					AttributesJSON:    mustJSON(ic),
					DiscoveredBy:      scanID,
					ManagedByProvider: ic.ConfigType == "GOOGLE_MANAGED",
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanSpannerInstancePartitions discovers instance partitions across every
// instance via the wildcard parent (SDK doc confirms `{instance} = '-'`
// support). Each page may mix partitions from multiple instances, so the
// owning Instance is derived per-row by splitting the partition's own
// resource name (KMS-style), rather than fanning out per already-scanned
// Instance.
func scanSpannerInstancePartitions(ctx context.Context, svc *spanner.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s/instances/-", p.ID)
	return runPaginated(ctx, st, p, "spanner:instancePartitions.list",
		svc.Projects.Instances.InstancePartitions.List(parent),
		func(page *spanner.ListInstancePartitionsResponse) (int, int, error) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, ip := range page.InstancePartitions {
				if ip == nil || ip.Name == "" {
					continue
				}
				instanceNative, _, ok := strings.Cut(ip.Name, "/instancePartitions/")
				if !ok {
					continue
				}
				name := lastSegment(ip.Name)
				region := lastSegment(ip.Config)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeSpannerInstancePartition,
					NativeID:       ip.Name,
					Name:           &name,
					Region:         strp(region),
					Status:         strp(ip.State),
					AttributesJSON: mustJSON(ip),
					DiscoveredBy:   scanID,
				})
				instID := store.ResourceID("gcp", p.ID, instanceNative)
				partID := store.ResourceID("gcp", p.ID, ip.Name)
				pairs = append(pairs, [2]string{partID, instID})
			}
			n, upErr := st.UpsertResources(batch)
			if upErr != nil {
				return 0, 0, fmt.Errorf("upsert spanner instance partitions: %w", upErr)
			}
			if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
				return 0, 0, fmt.Errorf("closure spanner instance partitions: %w", cErr)
			}
			return len(batch), n, nil
		})
}

// scanSpannerDatabases discovers databases per already-scanned Instance (no
// wildcard parent support). Returns every database's NativeID (across all
// instances) for phase's per-Database BackupSchedule/DatabaseRole fan-out.
func scanSpannerDatabases(ctx context.Context, svc *spanner.Service, p *project, instances []*spanner.Instance, st *store.Store, scanID string) (databaseNames []string, total, inserted int, err error) {
	for _, inst := range instances {
		var dbatch []*store.Resource
		listErr := svc.Projects.Instances.Databases.List(inst.Name).Pages(ctx, func(page *spanner.ListDatabasesResponse) error {
			for _, d := range page.Databases {
				if d == nil || d.Name == "" {
					continue
				}
				databaseNames = append(databaseNames, d.Name)
				name := lastSegment(d.Name)
				dbatch = append(dbatch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeSpannerDatabase,
					NativeID:       d.Name,
					Name:           &name,
					CreatedAt:      strp(d.CreateTime),
					Status:         strp(d.State),
					AttributesJSON: mustJSON(d),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				_ = skipIfDenied(st, "spanner:databases.list", p.ID, listErr)
				continue
			}
			return databaseNames, total, inserted, listErr
		}
		instResID := store.ResourceID("gcp", p.ID, inst.Name)
		dt, dn, derr := upsertWithParent(st, dbatch, instResID)
		total += dt
		inserted += dn
		if derr != nil {
			return databaseNames, total, inserted, derr
		}
	}
	return databaseNames, total, inserted, nil
}

// scanSpannerBackups fans out Backups.List per already-scanned Instance (no
// wildcard parent support).
func scanSpannerBackups(ctx context.Context, svc *spanner.Service, p *project, instances []*spanner.Instance, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentSpannerFanout, instances, func(gctx context.Context, inst *spanner.Instance) error {
		if inst == nil || inst.Name == "" {
			return nil
		}
		instResID := store.ResourceID("gcp", p.ID, inst.Name)
		var batch []*store.Resource
		listErr := svc.Projects.Instances.Backups.List(inst.Name).Pages(gctx, func(page *spanner.ListBackupsResponse) error {
			for _, b := range page.Backups {
				if b == nil || b.Name == "" {
					continue
				}
				name := lastSegment(b.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeSpannerBackup,
					NativeID:       b.Name,
					Name:           &name,
					CreatedAt:      strp(b.CreateTime),
					Status:         strp(b.State),
					AttributesJSON: mustJSON(b),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "spanner:backups.list", inst.Name, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, instResID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanSpannerBackupSchedules fans out BackupSchedules.List per already-
// scanned Database (no wildcard parent support).
func scanSpannerBackupSchedules(ctx context.Context, svc *spanner.Service, p *project, databaseNames []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentSpannerFanout, databaseNames, func(gctx context.Context, dbName string) error {
		dbResID := store.ResourceID("gcp", p.ID, dbName)
		var batch []*store.Resource
		listErr := svc.Projects.Instances.Databases.BackupSchedules.List(dbName).Pages(gctx, func(page *spanner.ListBackupSchedulesResponse) error {
			for _, bs := range page.BackupSchedules {
				if bs == nil || bs.Name == "" {
					continue
				}
				name := lastSegment(bs.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeSpannerBackupSchedule,
					NativeID:       bs.Name,
					Name:           &name,
					AttributesJSON: mustJSON(bs),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "spanner:backupSchedules.list", dbName, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, dbResID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanSpannerDatabaseRoles fans out DatabaseRoles.List per already-scanned
// Database (no wildcard parent support).
func scanSpannerDatabaseRoles(ctx context.Context, svc *spanner.Service, p *project, databaseNames []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentSpannerFanout, databaseNames, func(gctx context.Context, dbName string) error {
		dbResID := store.ResourceID("gcp", p.ID, dbName)
		var batch []*store.Resource
		listErr := svc.Projects.Instances.Databases.DatabaseRoles.List(dbName).Pages(gctx, func(page *spanner.ListDatabaseRolesResponse) error {
			for _, r := range page.DatabaseRoles {
				if r == nil || r.Name == "" {
					continue
				}
				name := lastSegment(r.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeSpannerDatabaseRole,
					NativeID:       r.Name,
					Name:           &name,
					AttributesJSON: mustJSON(r),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "spanner:databaseRoles.list", dbName, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, dbResID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}
