package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/sqladmin/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSQLInstance, Service: "sqladmin", Upstream: "sqladmin.googleapis.com/Instance", Redact: []redact.Rule{{Path: "rootPassword", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeSQLBackupRun, Service: "sqladmin"})
	registerType(restype.Descriptor{Type: TypeSQLDatabase, Service: "sqladmin", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSQLSslCert, Service: "sqladmin", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSQLUser, Service: "sqladmin", Redact: []redact.Rule{{Path: "password", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "gcp:sql",
		fn:   scanCloudSQL,
	})
}

// scanCloudSQL discovers Cloud SQL instances for a project, then fans out per
// instance into BackupRun/Database/SslCert/User (bounded concurrency —
// per-instance child fan-out, same shape as KMS's per-location fan-out).
// Uses Pages() so many-instance projects aren't silently truncated at the
// default page size.
func scanCloudSQL(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := sqladmin.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("sqladmin client: %w", err)
	}

	return runPaginated(ctx, st, p, "sqladmin:instances.list",
		svc.Instances.List(p.ID),
		func(page *sqladmin.InstancesListResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, inst := range page.Items {
				name := inst.Name
				region := inst.Region
				status := inst.State
				r := &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeSQLInstance,
					NativeID:       fmt.Sprintf("projects/%s/instances/%s", p.ID, name),
					Name:           &name,
					Region:         &region,
					CreatedAt:      strp(inst.CreateTime),
					Status:         &status,
					AttributesJSON: mustJSON(inst),
					DiscoveredBy:   scanID,
				}
				if len(inst.Settings.UserLabels) > 0 {
					s := mustJSON(inst.Settings.UserLabels)
					r.TagsJSON = &s
				}
				batch = append(batch, r)
			}
			if len(batch) == 0 {
				return 0, 0, nil
			}
			n, e := st.UpsertResources(batch)
			if e != nil {
				return 0, 0, fmt.Errorf("upsert Cloud SQL instances: %w", e)
			}
			total, inserted := len(batch), n

			var mu sync.Mutex
			fanErr := forEachItem(ctx, fanoutMed, page.Items, func(gctx context.Context, inst *sqladmin.DatabaseInstance) error {
				instID := store.ResourceID("gcp", p.ID, TypeSQLInstance, fmt.Sprintf("projects/%s/instances/%s", p.ID, inst.Name))
				t, ins, cErr := scanCloudSQLInstanceChildren(gctx, svc, p, inst.Name, instID, st, scanID)
				mu.Lock()
				total += t
				inserted += ins
				mu.Unlock()
				return cErr
			})
			if fanErr != nil {
				return total, inserted, fanErr
			}
			return total, inserted, nil
		})
}

// scanCloudSQLInstanceChildren fetches BackupRun (paginated), Database,
// SslCert, and User for one Cloud SQL instance. Each List call classifies its
// own permission errors via skipIfDenied — a denial on one child type (or one
// instance) never aborts scanning of siblings or other instances.
func scanCloudSQLInstanceChildren(ctx context.Context, svc *sqladmin.Service, p *project, instanceName, instID string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, n, err := scanCloudSQLBackupRuns(ctx, svc, p, instanceName, instID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanCloudSQLDatabases(ctx, svc, p, instanceName, instID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanCloudSQLSslCerts(ctx, svc, p, instanceName, instID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanCloudSQLUsers(ctx, svc, p, instanceName, instID, st, scanID)
	total += t
	inserted += n
	return total, inserted, err
}

func scanCloudSQLBackupRuns(ctx context.Context, svc *sqladmin.Service, p *project, instanceName, instID string, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.BackupRuns.List(p.ID, instanceName).Pages(ctx, func(page *sqladmin.BackupRunsListResponse) error {
		batch := make([]*store.Resource, 0, len(page.Items))
		for _, br := range page.Items {
			status := br.Status
			nativeID := fmt.Sprintf("projects/%s/instances/%s/backupRuns/%d", p.ID, instanceName, br.Id)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeSQLBackupRun,
				NativeID:       nativeID,
				Status:         &status,
				CreatedAt:      strp(br.EnqueuedTime),
				AttributesJSON: mustJSON(br),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithParent(st, batch, instID)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "sqladmin:backupRuns.list", instanceName, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

func scanCloudSQLDatabases(ctx context.Context, svc *sqladmin.Service, p *project, instanceName, instID string, st *store.Store, scanID string) (total, inserted int, err error) {
	resp, err := svc.Databases.List(p.ID, instanceName).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "sqladmin:databases.list", instanceName, err)
		}
		return 0, 0, err
	}
	batch := make([]*store.Resource, 0, len(resp.Items))
	for _, db := range resp.Items {
		name := db.Name
		nativeID := fmt.Sprintf("projects/%s/instances/%s/databases/%s", p.ID, instanceName, db.Name)
		batch = append(batch, &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           TypeSQLDatabase,
			NativeID:       nativeID,
			Name:           &name,
			AttributesJSON: mustJSON(db),
			DiscoveredBy:   scanID,
		})
	}
	return upsertWithParent(st, batch, instID)
}

func scanCloudSQLSslCerts(ctx context.Context, svc *sqladmin.Service, p *project, instanceName, instID string, st *store.Store, scanID string) (total, inserted int, err error) {
	resp, err := svc.SslCerts.List(p.ID, instanceName).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "sqladmin:sslCerts.list", instanceName, err)
		}
		return 0, 0, err
	}
	batch := make([]*store.Resource, 0, len(resp.Items))
	for _, sc := range resp.Items {
		name := sc.CommonName
		nativeID := fmt.Sprintf("projects/%s/instances/%s/sslCerts/%s", p.ID, instanceName, sc.Sha1Fingerprint)
		batch = append(batch, &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           TypeSQLSslCert,
			NativeID:       nativeID,
			Name:           &name,
			CreatedAt:      strp(sc.CreateTime),
			AttributesJSON: mustJSON(sc),
			DiscoveredBy:   scanID,
		})
	}
	return upsertWithParent(st, batch, instID)
}

func scanCloudSQLUsers(ctx context.Context, svc *sqladmin.Service, p *project, instanceName, instID string, st *store.Store, scanID string) (total, inserted int, err error) {
	resp, err := svc.Users.List(p.ID, instanceName).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "sqladmin:users.list", instanceName, err)
		}
		return 0, 0, err
	}
	batch := make([]*store.Resource, 0, len(resp.Items))
	for _, u := range resp.Items {
		name := u.Name
		nativeID := fmt.Sprintf("projects/%s/instances/%s/users/%s", p.ID, instanceName, u.Name)
		if u.Host != "" {
			nativeID += "@" + u.Host
		}
		batch = append(batch, &store.Resource{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           TypeSQLUser,
			NativeID:       nativeID,
			Name:           &name,
			AttributesJSON: mustJSON(u),
			DiscoveredBy:   scanID,
		})
	}
	return upsertWithParent(st, batch, instID)
}
