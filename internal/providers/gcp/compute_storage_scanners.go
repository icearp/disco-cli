package gcp

import (
	"context"
	"sync"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

// Wave 1 of the GCP type-coverage buildout (docs/gcp-type-coverage.md): the
// Compute Engine storage-resource domain. New phases of the existing
// "gcp:compute" service (wired into scanCompute's fan-out list in
// compute_scanners.go) — not a new service registration, so emits go through
// registerExtraEmits rather than a second registerService call.
func init() {
	registerType(restype.Descriptor{Type: TypeComputeDisk, Service: "compute", Upstream: "compute.googleapis.com/Disk"})
	registerType(restype.Descriptor{Type: TypeComputeRegionDisk, Service: "compute", Upstream: "compute.googleapis.com/RegionDisk"})
	registerType(restype.Descriptor{Type: TypeComputeImage, Service: "compute", Upstream: "compute.googleapis.com/Image"})
	registerType(restype.Descriptor{Type: TypeComputeMachineImage, Service: "compute", Upstream: "compute.googleapis.com/MachineImage"})
	registerType(restype.Descriptor{Type: TypeComputeSnapshot, Service: "compute", Upstream: "compute.googleapis.com/Snapshot"})
	registerType(restype.Descriptor{Type: TypeComputeRegionSnapshot, Service: "compute", Upstream: "compute.googleapis.com/RegionSnapshot"})
	registerType(restype.Descriptor{Type: TypeComputeInstantSnapshot, Service: "compute", Upstream: "compute.googleapis.com/InstantSnapshot"})
	registerType(restype.Descriptor{Type: TypeComputeRegionInstantSnapshot, Service: "compute", Upstream: "compute.googleapis.com/RegionInstantSnapshot"})
	registerType(restype.Descriptor{Type: TypeComputeInstantSnapshotGroup, Service: "compute", Upstream: "compute.googleapis.com/InstantSnapshotGroup"})
	registerType(restype.Descriptor{Type: TypeComputeRegionInstantSnapshotGroup, Service: "compute", Upstream: "compute.googleapis.com/RegionInstantSnapshotGroup"})
	registerType(restype.Descriptor{Type: TypeComputeStoragePool, Service: "compute", Upstream: "compute.googleapis.com/StoragePool", Leaf: true})
}

func scanComputeDisks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:disks.aggregatedList",
		svc.Disks.AggregatedList(p.ID),
		func(page *compute.DiskAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, d := range items.Disks {
					batch = append(batch, diskToResource(p, scanID, TypeComputeDisk, d))
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeRegionDisks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionDisks.list",
		func(region string) pager[compute.DiskList] { return svc.RegionDisks.List(p.ID, region) },
		func(page *compute.DiskList) []*compute.Disk { return page.Items },
		func(d *compute.Disk, region string) *store.Resource {
			r := diskToResource(p, scanID, TypeComputeRegionDisk, d)
			r.Region = &region
			r.Zone = nil
			return r
		})
}

func diskToResource(p *project, scanID, discoType string, d *compute.Disk) *store.Resource {
	r := &store.Resource{
		Provider:       "gcp",
		AccountID:      p.ID,
		AccountName:    &p.Name,
		Type:           discoType,
		NativeID:       d.SelfLink,
		Name:           &d.Name,
		CreatedAt:      strp(d.CreationTimestamp),
		Status:         strp(d.Status),
		AttributesJSON: mustJSON(d),
		DiscoveredBy:   scanID,
	}
	if d.Zone != "" {
		zone := lastSegment(d.Zone)
		r.Zone = &zone
		region := zoneToRegion(zone)
		r.Region = &region
	}
	return r
}

func scanComputeImages(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:images.list",
		svc.Images.List(p.ID),
		func(page *compute.ImageList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, img := range page.Items {
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeComputeImage,
					NativeID:       img.SelfLink,
					Name:           &img.Name,
					CreatedAt:      strp(img.CreationTimestamp),
					AttributesJSON: mustJSON(img),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeMachineImages(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:machineImages.list",
		svc.MachineImages.List(p.ID),
		func(page *compute.MachineImageList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, mi := range page.Items {
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeComputeMachineImage,
					NativeID:       mi.SelfLink,
					Name:           &mi.Name,
					CreatedAt:      strp(mi.CreationTimestamp),
					AttributesJSON: mustJSON(mi),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeSnapshots(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:snapshots.list",
		svc.Snapshots.List(p.ID),
		func(page *compute.SnapshotList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, snap := range page.Items {
				batch = append(batch, snapshotToResource(p, scanID, TypeComputeSnapshot, snap))
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeRegionSnapshots(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionSnapshots.list",
		func(region string) pager[compute.SnapshotList] { return svc.RegionSnapshots.List(p.ID, region) },
		func(page *compute.SnapshotList) []*compute.Snapshot { return page.Items },
		func(snap *compute.Snapshot, region string) *store.Resource {
			r := snapshotToResource(p, scanID, TypeComputeRegionSnapshot, snap)
			r.Region = &region
			return r
		})
}

func snapshotToResource(p *project, scanID, discoType string, snap *compute.Snapshot) *store.Resource {
	return &store.Resource{
		Provider:       "gcp",
		AccountID:      p.ID,
		AccountName:    &p.Name,
		Type:           discoType,
		NativeID:       snap.SelfLink,
		Name:           &snap.Name,
		CreatedAt:      strp(snap.CreationTimestamp),
		AttributesJSON: mustJSON(snap),
		DiscoveredBy:   scanID,
	}
}

func scanComputeInstantSnapshots(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:instantSnapshots.aggregatedList",
		svc.InstantSnapshots.AggregatedList(p.ID),
		func(page *compute.InstantSnapshotAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, is := range items.InstantSnapshots {
					batch = append(batch, instantSnapshotToResource(p, scanID, TypeComputeInstantSnapshot, is))
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeRegionInstantSnapshots(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionInstantSnapshots.list",
		func(region string) pager[compute.InstantSnapshotList] {
			return svc.RegionInstantSnapshots.List(p.ID, region)
		},
		func(page *compute.InstantSnapshotList) []*compute.InstantSnapshot { return page.Items },
		func(is *compute.InstantSnapshot, region string) *store.Resource {
			r := instantSnapshotToResource(p, scanID, TypeComputeRegionInstantSnapshot, is)
			r.Region = &region
			return r
		})
}

func instantSnapshotToResource(p *project, scanID, discoType string, is *compute.InstantSnapshot) *store.Resource {
	r := &store.Resource{
		Provider:       "gcp",
		AccountID:      p.ID,
		AccountName:    &p.Name,
		Type:           discoType,
		NativeID:       is.SelfLink,
		Name:           &is.Name,
		CreatedAt:      strp(is.CreationTimestamp),
		AttributesJSON: mustJSON(is),
		DiscoveredBy:   scanID,
	}
	if is.Zone != "" {
		zone := lastSegment(is.Zone)
		r.Zone = &zone
	}
	return r
}

// scanComputeInstantSnapshotGroups fans out over every zone (no
// AggregatedList exists for this type) via gcpZones + forEachItem. A
// one-off, not promoted to a shared gcpZoneFanoutScan generic — this is the
// only Wave 1 type needing per-zone (rather than per-region) fan-out.
func scanComputeInstantSnapshotGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	zones, err := gcpZones(ctx, p)
	if err != nil {
		return 0, 0, err
	}
	if len(zones) == 0 {
		return 0, 0, nil
	}
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	if err := forEachItem(ctx, fanoutMed, zones, func(gctx context.Context, zone string) error {
		perr := svc.InstantSnapshotGroups.List(p.ID, zone).Pages(gctx, func(page *compute.ListInstantSnapshotGroups) error {
			local := make([]*store.Resource, 0, len(page.Items))
			for _, g := range page.Items {
				local = append(local, instantSnapshotGroupToResource(p, scanID, TypeComputeInstantSnapshotGroup, g))
			}
			if len(local) > 0 {
				mu.Lock()
				batch = append(batch, local...)
				mu.Unlock()
			}
			return nil
		})
		if perr != nil {
			if isPermissionDenied(perr) {
				return skipIfDenied(st, "compute:instantSnapshotGroups.list", p.ID+"/"+zone, perr)
			}
			return perr
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	return upsertWithProjClosure(p, st, batch)
}

func scanComputeRegionInstantSnapshotGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionInstantSnapshotGroups.list",
		func(region string) pager[compute.ListInstantSnapshotGroups] {
			return svc.RegionInstantSnapshotGroups.List(p.ID, region)
		},
		func(page *compute.ListInstantSnapshotGroups) []*compute.InstantSnapshotGroup { return page.Items },
		func(g *compute.InstantSnapshotGroup, region string) *store.Resource {
			r := instantSnapshotGroupToResource(p, scanID, TypeComputeRegionInstantSnapshotGroup, g)
			r.Region = &region
			return r
		})
}

func instantSnapshotGroupToResource(p *project, scanID, discoType string, g *compute.InstantSnapshotGroup) *store.Resource {
	r := &store.Resource{
		Provider:       "gcp",
		AccountID:      p.ID,
		AccountName:    &p.Name,
		Type:           discoType,
		NativeID:       g.SelfLink,
		Name:           &g.Name,
		CreatedAt:      strp(g.CreationTimestamp),
		AttributesJSON: mustJSON(g),
		DiscoveredBy:   scanID,
	}
	if g.Zone != "" {
		zone := lastSegment(g.Zone)
		r.Zone = &zone
	}
	return r
}

func scanComputeStoragePools(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:storagePools.aggregatedList",
		svc.StoragePools.AggregatedList(p.ID),
		func(page *compute.StoragePoolAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, sp := range items.StoragePools {
					r := &store.Resource{
						Provider:       "gcp",
						AccountID:      p.ID,
						AccountName:    &p.Name,
						Type:           TypeComputeStoragePool,
						NativeID:       sp.SelfLink,
						Name:           &sp.Name,
						CreatedAt:      strp(sp.CreationTimestamp),
						AttributesJSON: mustJSON(sp),
						DiscoveredBy:   scanID,
					}
					if sp.Zone != "" {
						zone := lastSegment(sp.Zone)
						r.Zone = &zone
						region := zoneToRegion(zone)
						r.Region = &region
					}
					batch = append(batch, r)
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}
