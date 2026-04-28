package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/bigtableadmin/v2"
	"google.golang.org/api/firestore/v1"
	"google.golang.org/api/spanner/v1"
)

func init() {
	registerService(serviceEntry{name: "gcp:bigtable", fn: scanBigtable})
	registerService(serviceEntry{name: "gcp:firestore", fn: scanFirestore})
	registerService(serviceEntry{name: "gcp:spanner", fn: scanSpanner})
}

// scanBigtable discovers Bigtable instances and their clusters. Tables and
// app-profiles deferred — table cardinality is unbounded; app-profiles need
// per-cluster context that the cluster CMEK story doesn't yet require.
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
		if len(cbatch) == 0 {
			continue
		}
		cn, cerr := st.UpsertResources(cbatch)
		if cerr != nil {
			return total, inserted, cerr
		}
		total += len(cbatch)
		inserted += cn
		// Closure: cluster → instance.
		instResID := store.ResourceID("gcp", p.ID, TypeBigtableInstance, inst.Name)
		pairs := make([][2]string, 0, len(cbatch))
		for _, r := range cbatch {
			pairs = append(pairs, [2]string{
				store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID),
				instResID,
			})
		}
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return total, inserted, err
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

// scanSpanner discovers Spanner instances + databases. Per-database backups
// + sessions deferred — backups are point-in-time copies (cardinality
// proportional to retention policy), sessions are runtime objects.
func scanSpanner(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := spanner.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("spanner client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s", p.ID)
	var instances []*spanner.Instance
	t, n, err := runPaginated(ctx, st, p, "spanner:instances.list",
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
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Per-instance databases.
	for _, inst := range instances {
		dresp, err := svc.Projects.Instances.Databases.List(inst.Name).Context(ctx).Do()
		if err != nil {
			if isPermissionDenied(err) {
				_ = skipIfDenied(st, "spanner:databases.list", p.ID, err)
				continue
			}
			return total, inserted, err
		}
		var dbatch []*store.Resource
		for _, d := range dresp.Databases {
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
		if len(dbatch) == 0 {
			continue
		}
		dn, derr := st.UpsertResources(dbatch)
		if derr != nil {
			return total, inserted, derr
		}
		total += len(dbatch)
		inserted += dn
		instResID := store.ResourceID("gcp", p.ID, TypeSpannerInstance, inst.Name)
		pairs := make([][2]string, 0, len(dbatch))
		for _, r := range dbatch {
			pairs = append(pairs, [2]string{
				store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID),
				instResID,
			})
		}
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
}
