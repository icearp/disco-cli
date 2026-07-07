package gcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/bigtableadmin/v2"
	"google.golang.org/api/firestore/v1"
	"google.golang.org/api/spanner/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:bigtable",
		fn:   scanBigtable,
		emits: []coverage.TypeDecl{
			{Service: "bigtableadmin", DiscoType: TypeBigtableInstance},
			{Service: "bigtableadmin", DiscoType: TypeBigtableCluster},
		},
	})
	registerService(serviceEntry{
		name: "gcp:firestore",
		fn:   scanFirestore,
		emits: []coverage.TypeDecl{
			{Service: "firestore", DiscoType: TypeFirestoreDB},
		},
	})
	registerService(serviceEntry{
		name: "gcp:spanner",
		fn:   scanSpanner,
		emits: []coverage.TypeDecl{
			{Service: "spanner", DiscoType: TypeSpannerInstance},
			{Service: "spanner", DiscoType: TypeSpannerDatabase},
			{Service: "spanner", DiscoType: TypeSpannerInstanceConfig},
			{Service: "spanner", DiscoType: TypeSpannerInstancePartition},
			{Service: "spanner", DiscoType: TypeSpannerBackup},
			{Service: "spanner", DiscoType: TypeSpannerBackupSchedule},
			{Service: "spanner", DiscoType: TypeSpannerDatabaseRole},
		},
	})
}

// maxConcurrentSpannerFanout caps per-Instance Backup and per-Database
// BackupSchedule/DatabaseRole fan-out. Per-project cardinality is low; keep
// modest like the DNS/logging/monitoring per-item fan-outs.
const maxConcurrentSpannerFanout = 10

// scanBigtable discovers Bigtable instances and their clusters. Tables and
// app-profiles deferred — table cardinality is unbounded; app-profiles need
// per-cluster context the cluster CMEK story doesn't yet require.
func scanBigtable(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := bigtableadmin.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("bigtableadmin client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s", p.ID)

	resp, err := svc.Projects.Instances.List(parent).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "bigtableadmin:instances.list", p.ID, err)
		}
		return 0, 0, err
	}
	var batch []*store.Resource
	for _, inst := range resp.Instances {
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
	t, n, e := upsertWithProjClosure(p, st, batch)
	total += t
	inserted += n
	if e != nil {
		return total, inserted, e
	}

	// Per-instance clusters — sequential because instance counts are tiny.
	for _, inst := range resp.Instances {
		cresp, err := svc.Projects.Instances.Clusters.List(inst.Name).Context(ctx).Do()
		if err != nil {
			if isPermissionDenied(err) {
				_ = skipIfDenied(st, "bigtableadmin:clusters.list", p.ID, err)
				continue
			}
			return total, inserted, err
		}
		var cbatch []*store.Resource
		for _, c := range cresp.Clusters {
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
		instResID := store.ResourceID("gcp", p.ID, TypeBigtableInstance, inst.Name)
		ct, cn, cerr := upsertWithParent(st, cbatch, instResID)
		total += ct
		inserted += cn
		if cerr != nil {
			return total, inserted, cerr
		}
	}
	return total, inserted, nil
}

// scanFirestore discovers Firestore databases (multi-DB project support).
// Indexes / collection groups / fields deferred — narrow graph value vs.
// per-database fan-out cost.
func scanFirestore(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := firestore.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("firestore client: %w", err)
	}
	resp, err := svc.Projects.Databases.List(fmt.Sprintf("projects/%s", p.ID)).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "firestore:databases.list", p.ID, err)
		}
		return 0, 0, err
	}
	var batch []*store.Resource
	for _, d := range resp.Databases {
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
	t, n, e := upsertWithProjClosure(p, st, batch)
	return t, n, e
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
				instID := store.ResourceID("gcp", p.ID, TypeSpannerInstance, instanceNative)
				partID := store.ResourceID("gcp", p.ID, TypeSpannerInstancePartition, ip.Name)
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
		instResID := store.ResourceID("gcp", p.ID, TypeSpannerInstance, inst.Name)
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
		instResID := store.ResourceID("gcp", p.ID, TypeSpannerInstance, inst.Name)
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
		dbResID := store.ResourceID("gcp", p.ID, TypeSpannerDatabase, dbName)
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
		dbResID := store.ResourceID("gcp", p.ID, TypeSpannerDatabase, dbName)
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
