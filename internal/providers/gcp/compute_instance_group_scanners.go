package gcp

import (
	"context"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

// Wave 2 of the GCP type-coverage buildout (docs/gcp-type-coverage.md): the
// Compute Engine instance-groups & templates domain. New phases of the
// existing "gcp:compute" service (wired into scanCompute's fan-out list in
// compute_scanners.go).
func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeInstanceGroup},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionInstanceGroup},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeInstanceGroupManager},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionInstanceGroupManager},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeInstanceGroupManagerResizeRequest},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionInstanceGroupManagerResizeRequest},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeInstanceTemplate},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionInstanceTemplate},
	)
}

func scanComputeInstanceGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:instanceGroups.aggregatedList",
		svc.InstanceGroups.AggregatedList(p.ID),
		func(page *compute.InstanceGroupAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, ig := range items.InstanceGroups {
					batch = append(batch, instanceGroupToResource(p, scanID, TypeComputeInstanceGroup, ig))
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeRegionInstanceGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionInstanceGroups.list",
		func(region string) pager[compute.RegionInstanceGroupList] {
			return svc.RegionInstanceGroups.List(p.ID, region)
		},
		func(page *compute.RegionInstanceGroupList) []*compute.InstanceGroup { return page.Items },
		func(ig *compute.InstanceGroup, region string) *store.Resource {
			r := instanceGroupToResource(p, scanID, TypeComputeRegionInstanceGroup, ig)
			r.Region = &region
			r.Zone = nil
			return r
		})
}

func instanceGroupToResource(p *project, scanID, discoType string, ig *compute.InstanceGroup) *store.Resource {
	r := &store.Resource{
		Provider:       "gcp",
		AccountID:      p.ID,
		AccountName:    &p.Name,
		Type:           discoType,
		NativeID:       ig.SelfLink,
		Name:           &ig.Name,
		CreatedAt:      strp(ig.CreationTimestamp),
		AttributesJSON: mustJSON(ig),
		DiscoveredBy:   scanID,
	}
	if ig.Zone != "" {
		zone := lastSegment(ig.Zone)
		r.Zone = &zone
		region := zoneToRegion(zone)
		r.Region = &region
	}
	return r
}

// igmRef identifies an already-discovered instance group manager so a
// follow-up phase can fan out its nested ResizeRequests.List call — mirrors
// the two-phase list-then-fan-out shape in bigquery_scanners.go.
type igmRef struct {
	scope string // zone or region name
	name  string
}

func scanComputeInstanceGroupManagers(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	var refs []igmRef
	t, n, err := runPaginated(ctx, st, p, "compute:instanceGroupManagers.aggregatedList",
		svc.InstanceGroupManagers.AggregatedList(p.ID),
		func(page *compute.InstanceGroupManagerAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, igm := range items.InstanceGroupManagers {
					batch = append(batch, instanceGroupManagerToResource(p, scanID, TypeComputeInstanceGroupManager, igm))
					if igm.Zone != "" {
						refs = append(refs, igmRef{scope: lastSegment(igm.Zone), name: igm.Name})
					}
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
	if err != nil {
		return t, n, err
	}
	t2, n2, err := scanComputeInstanceGroupManagerResizeRequests(ctx, svc, p, st, scanID, refs)
	return t + t2, n + n2, err
}

func scanComputeInstanceGroupManagerResizeRequests(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string, refs []igmRef) (total, inserted int, err error) {
	if len(refs) == 0 {
		return 0, 0, nil
	}
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	if err := forEachItem(ctx, fanoutMed, refs, func(gctx context.Context, ref igmRef) error {
		perr := svc.InstanceGroupManagerResizeRequests.List(p.ID, ref.scope, ref.name).Pages(gctx, func(page *compute.InstanceGroupManagerResizeRequestsListResponse) error {
			local := make([]*store.Resource, 0, len(page.Items))
			for _, rr := range page.Items {
				r := resizeRequestToResource(p, scanID, TypeComputeInstanceGroupManagerResizeRequest, rr)
				r.Zone = &ref.scope
				local = append(local, r)
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
				return skipIfDenied(st, "compute:instanceGroupManagerResizeRequests.list", p.ID+"/"+ref.scope+"/"+ref.name, perr)
			}
			return perr
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	return upsertWithProjClosure(p, st, batch)
}

func scanComputeRegionInstanceGroupManagers(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	regions, err := gcpRegions(ctx, p)
	if err != nil {
		return 0, 0, err
	}
	if len(regions) == 0 {
		return 0, 0, nil
	}
	var (
		mu   sync.Mutex
		refs []igmRef
	)
	t, n, err := gcpRegionFanoutScanIn(ctx, p, st, fanoutMed, regions, "compute:regionInstanceGroupManagers.list",
		func(region string) pager[compute.RegionInstanceGroupManagerList] {
			return svc.RegionInstanceGroupManagers.List(p.ID, region)
		},
		func(page *compute.RegionInstanceGroupManagerList) []*compute.InstanceGroupManager { return page.Items },
		func(igm *compute.InstanceGroupManager, region string) *store.Resource {
			mu.Lock()
			refs = append(refs, igmRef{scope: region, name: igm.Name})
			mu.Unlock()
			r := instanceGroupManagerToResource(p, scanID, TypeComputeRegionInstanceGroupManager, igm)
			r.Region = &region
			r.Zone = nil
			return r
		})
	if err != nil {
		return t, n, err
	}
	t2, n2, err := scanComputeRegionInstanceGroupManagerResizeRequests(ctx, svc, p, st, scanID, refs)
	return t + t2, n + n2, err
}

func scanComputeRegionInstanceGroupManagerResizeRequests(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string, refs []igmRef) (total, inserted int, err error) {
	if len(refs) == 0 {
		return 0, 0, nil
	}
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	if err := forEachItem(ctx, fanoutMed, refs, func(gctx context.Context, ref igmRef) error {
		perr := svc.RegionInstanceGroupManagerResizeRequests.List(p.ID, ref.scope, ref.name).Pages(gctx, func(page *compute.RegionInstanceGroupManagerResizeRequestsListResponse) error {
			local := make([]*store.Resource, 0, len(page.Items))
			for _, rr := range page.Items {
				r := resizeRequestToResource(p, scanID, TypeComputeRegionInstanceGroupManagerResizeRequest, rr)
				r.Region = &ref.scope
				local = append(local, r)
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
				return skipIfDenied(st, "compute:regionInstanceGroupManagerResizeRequests.list", p.ID+"/"+ref.scope+"/"+ref.name, perr)
			}
			return perr
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	return upsertWithProjClosure(p, st, batch)
}

func instanceGroupManagerToResource(p *project, scanID, discoType string, igm *compute.InstanceGroupManager) *store.Resource {
	r := &store.Resource{
		Provider:       "gcp",
		AccountID:      p.ID,
		AccountName:    &p.Name,
		Type:           discoType,
		NativeID:       igm.SelfLink,
		Name:           &igm.Name,
		CreatedAt:      strp(igm.CreationTimestamp),
		AttributesJSON: mustJSON(igm),
		DiscoveredBy:   scanID,
	}
	if igm.Zone != "" {
		zone := lastSegment(igm.Zone)
		r.Zone = &zone
		region := zoneToRegion(zone)
		r.Region = &region
	}
	return r
}

// resizeRequestToResource shapes an InstanceGroupManagerResizeRequest — the
// zonal and regional list responses (InstanceGroupManagerResizeRequestsList,
// RegionInstanceGroupManagerResizeRequestsList) both return this same item
// type; only the enclosing *Service/*Call names are region-prefixed.
func resizeRequestToResource(p *project, scanID, discoType string, rr *compute.InstanceGroupManagerResizeRequest) *store.Resource {
	return &store.Resource{
		Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
		Type: discoType, NativeID: rr.SelfLink, Name: &rr.Name,
		CreatedAt: strp(rr.CreationTimestamp), AttributesJSON: mustJSON(rr),
		DiscoveredBy: scanID,
	}
}

// scanComputeInstanceTemplates covers both InstanceTemplate (global) and
// RegionInstanceTemplate (regional) via a single AggregatedList call — unlike
// Disk/Snapshot's zonal-only AggregatedList, InstanceTemplates.AggregatedList
// documents itself as returning "regional and global" resources under
// "global" / "regions/{region}" scope keys, so no second per-region List
// call is needed.
func scanComputeInstanceTemplates(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:instanceTemplates.aggregatedList",
		svc.InstanceTemplates.AggregatedList(p.ID),
		func(page *compute.InstanceTemplateAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				for _, it := range items.InstanceTemplates {
					discoType := TypeComputeInstanceTemplate
					var region *string
					if region0 := scopedListRegion(scope); region0 != "" {
						discoType = TypeComputeRegionInstanceTemplate
						region = &region0
					}
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: discoType, NativeID: it.SelfLink, Name: &it.Name,
						Region:         region,
						CreatedAt:      strp(it.CreationTimestamp),
						AttributesJSON: mustJSON(it),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}
