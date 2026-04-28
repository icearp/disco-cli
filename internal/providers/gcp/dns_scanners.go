package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
	"google.golang.org/api/dns/v1"
)

func init() { registerService(serviceEntry{name: "gcp:clouddns", fn: scanCloudDNS}) }

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
// `{zoneNativeID}/rrsets/{type}/{name}`. Both `name` and `type` are needed
// because (name, type) is the natural key (one zone can have, e.g.,
// www.example.com. with both A and AAAA record sets).
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
	if err := svc.ManagedZones.List(p.ID).Pages(ctx, func(page *dns.ManagedZonesListResponse) error {
		var batch []*store.Resource
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
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "dns:managedZones.list", p.ID, err)
		}
		return 0, 0, err
	}

	// Phase 2: per-zone record sets.
	sem := semaphore.NewWeighted(maxConcurrentDNSZones)
	g, gctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	for _, z := range zones {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
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
				if len(batch) == 0 {
					return nil
				}
				mu.Lock()
				defer mu.Unlock()
				rn, e := st.UpsertResources(batch)
				if e != nil {
					return e
				}
				total += len(batch)
				inserted += rn
				// Closure: record-set → managed-zone.
				zoneResID := store.ResourceID("gcp", p.ID, TypeDNSManagedZone, z.nativeID)
				pairs := make([][2]string, 0, len(batch))
				for _, r := range batch {
					pairs = append(pairs, [2]string{
						store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID),
						zoneResID,
					})
				}
				return st.BatchAddToHierarchyClosure(pairs)
			})
			if err != nil && isPermissionDenied(err) {
				return skipIfDenied(st, "dns:resourceRecordSets.list", p.ID, err)
			}
			return err
		})
	}
	return total, inserted, g.Wait()
}
