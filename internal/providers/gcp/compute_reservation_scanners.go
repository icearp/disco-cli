package gcp

import (
	"context"
	"strings"
	"sync"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/compute/v1"
)

// Wave 7 of the GCP type-coverage buildout (docs/gcp-type-coverage.md):
// Compute Engine autoscaling and capacity-reservation resources. New phases
// of "gcp:compute" (registerExtraEmits, same as compute_lb_ext_scanners.go).
//
// ReservationSlot (the fourth nesting level under Reservation ->
// ReservationBlock -> ReservationSubBlock -> ReservationSlot) is
// intentionally NOT scanned: it models individual physical capacity slots
// with no edges of its own and unbounded per-subblock cardinality (can run
// into the thousands for large ML/TPU reservations) — see
// docs/gcp-type-coverage.md for the DEFER note.
func init() {
	registerType(restype.Descriptor{Type: TypeComputeAutoscaler, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeRegionAutoscaler, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeReservation, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeReservationBlock, Service: "compute", Leaf: true})
	registerType(restype.Descriptor{Type: TypeComputeReservationSubBlock, Service: "compute", Leaf: true})
	registerType(restype.Descriptor{Type: TypeComputeFutureReservation, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeRegionCommitment, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeResourcePolicy, Service: "compute", Leaf: true})
	registerType(restype.Descriptor{Type: TypeComputeRegionSecurityPolicy, Service: "compute", Leaf: true})
}

// scanComputeAutoscalers covers both Autoscaler (zonal) and RegionAutoscaler
// (regional) via one combined-scope AggregatedList call — same dual-type
// split as scanComputeHealthChecks, but split on zonal vs regional scope
// keys instead of global vs regional.
func scanComputeAutoscalers(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:autoscalers.aggregatedList",
		svc.Autoscalers.AggregatedList(p.ID),
		func(page *compute.AutoscalerAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, as := range items.Autoscalers {
					r := &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						NativeID: as.SelfLink, Name: &as.Name,
						CreatedAt: strp(as.CreationTimestamp), AttributesJSON: mustJSON(as),
						DiscoveredBy: scanID,
					}
					if as.Zone != "" {
						zone := lastSegment(as.Zone)
						region := zoneToRegion(zone)
						r.Type = TypeComputeAutoscaler
						r.Zone = &zone
						r.Region = &region
					} else {
						region := lastSegment(as.Region)
						r.Type = TypeComputeRegionAutoscaler
						r.Region = &region
					}
					batch = append(batch, r)
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// reservationRef identifies a discovered reservation so a follow-up phase can
// fan out its nested ReservationBlocks.List call — mirrors igmRef.
type reservationRef struct {
	zone string
	name string
}

// reservationBlockRef identifies a discovered reservation block so a
// follow-up phase can fan out its nested ReservationSubBlocks.List call.
type reservationBlockRef struct {
	zone       string
	parentName string // "reservations/{reservation}/reservationBlocks/{block}"
}

func scanComputeReservations(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	var refs []reservationRef
	t, n, err := runPaginated(ctx, st, p, "compute:reservations.aggregatedList",
		svc.Reservations.AggregatedList(p.ID),
		func(page *compute.ReservationAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, res := range items.Reservations {
					r := &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeReservation, NativeID: res.SelfLink, Name: &res.Name,
						CreatedAt: strp(res.CreationTimestamp), AttributesJSON: mustJSON(res),
						DiscoveredBy: scanID,
					}
					if res.Zone != "" {
						zone := lastSegment(res.Zone)
						region := zoneToRegion(zone)
						r.Zone = &zone
						r.Region = &region
						refs = append(refs, reservationRef{zone: zone, name: res.Name})
					}
					batch = append(batch, r)
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
	if err != nil {
		return t, n, err
	}
	t2, n2, err := scanComputeReservationBlocks(ctx, svc, p, st, scanID, refs)
	return t + t2, n + n2, err
}

func scanComputeReservationBlocks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string, refs []reservationRef) (total, inserted int, err error) {
	if len(refs) == 0 {
		return 0, 0, nil
	}
	var (
		mu        sync.Mutex
		batch     []*store.Resource
		blockRefs []reservationBlockRef
	)
	if err := forEachItem(ctx, fanoutMed, refs, func(gctx context.Context, ref reservationRef) error {
		perr := svc.ReservationBlocks.List(p.ID, ref.zone, ref.name).Pages(gctx, func(page *compute.ReservationBlocksListResponse) error {
			local := make([]*store.Resource, 0, len(page.Items))
			var localRefs []reservationBlockRef
			for _, blk := range page.Items {
				local = append(local, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeReservationBlock, NativeID: blk.SelfLink, Name: &blk.Name,
					Zone:           &ref.zone,
					CreatedAt:      strp(blk.CreationTimestamp),
					AttributesJSON: mustJSON(blk),
					DiscoveredBy:   scanID,
				})
				localRefs = append(localRefs, reservationBlockRef{
					zone:       ref.zone,
					parentName: "reservations/" + ref.name + "/reservationBlocks/" + blk.Name,
				})
			}
			if len(local) > 0 {
				mu.Lock()
				batch = append(batch, local...)
				blockRefs = append(blockRefs, localRefs...)
				mu.Unlock()
			}
			return nil
		})
		if perr != nil {
			if isPermissionDenied(perr) {
				return skipIfDenied(st, "compute:reservationBlocks.list", p.ID+"/"+ref.zone+"/"+ref.name, perr)
			}
			return perr
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	// Parent is the reservation, not the project — derive its NativeID by
	// stripping the "/reservationBlocks/{block}" suffix off each block's own
	// SelfLink (same suffix-strip technique as kms_scanners.go's
	// cryptoKey → keyRing closure).
	t, n, err := upsertWithSuffixParent(st, batch, TypeComputeReservation, "/reservationBlocks/")
	if err != nil {
		return t, n, err
	}
	t2, n2, err := scanComputeReservationSubBlocks(ctx, svc, p, st, scanID, blockRefs)
	return t + t2, n + n2, err
}

func scanComputeReservationSubBlocks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string, refs []reservationBlockRef) (total, inserted int, err error) {
	if len(refs) == 0 {
		return 0, 0, nil
	}
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	if err := forEachItem(ctx, fanoutMed, refs, func(gctx context.Context, ref reservationBlockRef) error {
		perr := svc.ReservationSubBlocks.List(p.ID, ref.zone, ref.parentName).Pages(gctx, func(page *compute.ReservationSubBlocksListResponse) error {
			local := make([]*store.Resource, 0, len(page.Items))
			for _, sb := range page.Items {
				local = append(local, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeReservationSubBlock, NativeID: sb.SelfLink, Name: &sb.Name,
					Zone:           &ref.zone,
					CreatedAt:      strp(sb.CreationTimestamp),
					AttributesJSON: mustJSON(sb),
					DiscoveredBy:   scanID,
				})
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
				return skipIfDenied(st, "compute:reservationSubBlocks.list", p.ID+"/"+ref.zone+"/"+ref.parentName, perr)
			}
			return perr
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}
	// Parent is the reservation block, not the project — strip the
	// "/reservationSubBlocks/{sub}" suffix off each sub-block's own SelfLink.
	return upsertWithSuffixParent(st, batch, TypeComputeReservationBlock, "/reservationSubBlocks/")
}

// upsertWithSuffixParent upserts a batch whose true parent is derivable from
// each row's own NativeID by cutting at the first occurrence of suffixMarker
// (e.g. a child SelfLink "…/reservations/r1/reservationBlocks/b1" parents to
// reservation "…/reservations/r1" by cutting at "/reservationBlocks/").
// Use when a single upsertWithParent call can't apply — each row in the
// batch has a different immediate parent, unlike upsertWithProjClosure /
// upsertWithParent's single shared parent.
func upsertWithSuffixParent(st *store.Store, batch []*store.Resource, parentType, suffixMarker string) (int, int, error) {
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, err
	}
	pairs := make([][2]string, 0, len(batch))
	for _, r := range batch {
		parentNative := r.NativeID
		if i := strings.Index(parentNative, suffixMarker); i >= 0 {
			parentNative = parentNative[:i]
		}
		pairs = append(pairs, [2]string{
			store.ResourceID(r.Provider, r.AccountID, r.NativeID),
			store.ResourceID(r.Provider, r.AccountID, parentNative),
		})
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return len(batch), n, err
	}
	return len(batch), n, nil
}

func scanComputeFutureReservations(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:futureReservations.aggregatedList",
		svc.FutureReservations.AggregatedList(p.ID),
		func(page *compute.FutureReservationsAggregatedListResponse) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, fr := range items.FutureReservations {
					r := &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeFutureReservation, NativeID: fr.SelfLink, Name: &fr.Name,
						CreatedAt: strp(fr.CreationTimestamp), AttributesJSON: mustJSON(fr),
						DiscoveredBy: scanID,
					}
					if fr.Zone != "" {
						zone := lastSegment(fr.Zone)
						region := zoneToRegion(zone)
						r.Zone = &zone
						r.Region = &region
					}
					batch = append(batch, r)
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeRegionCommitments(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:regionCommitments.aggregatedList",
		svc.RegionCommitments.AggregatedList(p.ID),
		func(page *compute.CommitmentAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, c := range items.Commitments {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeRegionCommitment, NativeID: c.SelfLink, Name: &c.Name,
						Region:         &region,
						CreatedAt:      strp(c.CreationTimestamp),
						AttributesJSON: mustJSON(c),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeResourcePolicies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:resourcePolicies.aggregatedList",
		svc.ResourcePolicies.AggregatedList(p.ID),
		func(page *compute.ResourcePolicyAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, rp := range items.ResourcePolicies {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeResourcePolicy, NativeID: rp.SelfLink, Name: &rp.Name,
						Region:         &region,
						CreatedAt:      strp(rp.CreationTimestamp),
						AttributesJSON: mustJSON(rp),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeRegionSecurityPolicies has no combined-scope AggregatedList —
// RegionSecurityPoliciesService.List is a genuinely separate per-region
// endpoint reusing the same SecurityPolicyList/SecurityPolicy SDK types as
// the pre-existing global scanner (cloudarmor_scanners.go), so it needs its
// own gcpRegionFanoutScan-based scanner with the Region-prefixed disco type.
func scanComputeRegionSecurityPolicies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionSecurityPolicies.list",
		func(region string) pager[compute.SecurityPolicyList] {
			return svc.RegionSecurityPolicies.List(p.ID, region)
		},
		func(page *compute.SecurityPolicyList) []*compute.SecurityPolicy { return page.Items },
		func(sp *compute.SecurityPolicy, region string) *store.Resource {
			return &store.Resource{
				Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
				Type: TypeComputeRegionSecurityPolicy, NativeID: sp.SelfLink, Name: &sp.Name,
				Region:         &region,
				CreatedAt:      strp(sp.CreationTimestamp),
				AttributesJSON: mustJSON(sp),
				DiscoveredBy:   scanID,
			}
		})
}
