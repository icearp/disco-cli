package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/dns/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:clouddns",
		fn:   scanCloudDNS,
		emits: []coverage.TypeDecl{
			{Service: "dns", DiscoType: TypeDNSManagedZone},
			{Service: "dns", DiscoType: TypeDNSRecordSet},
		},
	})
}

// maxConcurrentDNSZones caps the per-project zone fan-out for record-set
// listing. Cloud DNS quotas are per-minute project-level — keep modest.
const maxConcurrentDNSZones = 10

// scanCloudDNS discovers Cloud DNS managed zones and their resource record
// sets. Two phases:
//  1. ManagedZones.List paginated.
//  2. Per-zone ResourceRecordSets.List, fan-out bounded by
//     maxConcurrentDNSZones.
//
// RecordSets have no API-issued resource name — synthesized NativeID is
// `{zoneNativeID}/rrsets/{type}/{name}`. Both `name` and `type` are needed:
// (name, type) is the natural key — one zone can have, e.g., www.example.com.
// as both A and AAAA record sets.
//
// DNSSEC keys + policies + response policy rules deferred — narrow graph
// value vs. cardinality risk; revisit if rule-engine demand surfaces.
func scanCloudDNS(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := dns.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("dns client: %w", err)
	}

	// Phase 1: managed zones.
	type zoneRef struct {
		nativeID string
		name     string
	}
	var zones []zoneRef
	t, n, err := runPaginated(ctx, st, p, "dns:managedZones.list",
		svc.ManagedZones.List(p.ID),
		func(page *dns.ManagedZonesListResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.ManagedZones))
			for _, z := range page.ManagedZones {
				zid := fmt.Sprintf("projects/%s/managedZones/%s", p.ID, z.Name)
				zname := z.Name
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeDNSManagedZone,
					NativeID:       zid,
					Name:           &zname,
					CreatedAt:      strp(z.CreationTime),
					AttributesJSON: mustJSON(z),
					DiscoveredBy:   scanID,
				})
				zones = append(zones, zoneRef{nativeID: zid, name: z.Name})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 2: per-zone record sets.
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentDNSZones, zones, func(gctx context.Context, z zoneRef) error {
		err := svc.ResourceRecordSets.List(p.ID, z.name).Pages(gctx, func(page *dns.ResourceRecordSetsListResponse) error {
			var batch []*store.Resource
			for _, rr := range page.Rrsets {
				nativeID := fmt.Sprintf("%s/rrsets/%s/%s", z.nativeID, rr.Type, rr.Name)
				name := fmt.Sprintf("%s %s", rr.Name, rr.Type)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeDNSRecordSet,
					NativeID:       nativeID,
					Name:           &name,
					AttributesJSON: mustJSON(rr),
					DiscoveredBy:   scanID,
				})
			}
			mu.Lock()
			defer mu.Unlock()
			rt, rn, rerr := upsertWithParent(st, batch, store.ResourceID("gcp", p.ID, TypeDNSManagedZone, z.nativeID))
			total += rt
			inserted += rn
			return rerr
		})
		if err != nil && isPermissionDenied(err) {
			return skipIfDenied(st, "dns:resourceRecordSets.list", p.ID, err)
		}
		return err
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}
