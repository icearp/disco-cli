package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name: "azure:dns",
		fn:   scanDNS,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.network", DiscoType: TypeDNSZone},
			{Service: "microsoft.network", DiscoType: TypeDNSRecordSet},
			{Service: "microsoft.network", DiscoType: TypeDNSPrivateZone},
			{Service: "microsoft.network", DiscoType: TypeDNSPrivateRecordSet},
			{Service: "microsoft.network", DiscoType: TypeDNSPrivateZoneVNetLink},
		},
	})
}

// scanDNS discovers Azure public DNS zones, private DNS zones, and the
// virtual-network links per private zone. Record sets are intentionally
// deferred — record-set fan-out per zone is unbounded volume; the
// record→target resolver story (A/CNAME → IP/host) warrants a separate
// iteration with rate-limited fan-out and target-type pluggability.
func scanDNS(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	zonesClient, err := armdns.NewZonesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdns:NewZonesClient: %w", err)
	}
	type pubZoneRef struct {
		rg, name, discoID string
	}
	var pubZones []pubZoneRef
	zt, zi, err := azSimpleScan(ctx, "armdns:Zones.List", TypeDNSZone, sub, st, scanID,
		zonesClient.NewListPager(nil),
		func(p armdns.ZonesClientListResponse) []*armdns.Zone { return p.Value },
		func(z *armdns.Zone) azTrackedBase {
			b := azTrackedBase{id: sv(z.ID), name: sv(z.Name), location: sv(z.Location), tags: z.Tags, full: z}
			if rg := rgNameFromID(b.id); rg != "" {
				pubZones = append(pubZones, pubZoneRef{
					rg: rg, name: b.name,
					discoID: store.ResourceID("azure", sub.ID, TypeDNSZone, b.id),
				})
			}
			return b
		})
	total += zt
	inserted += zi
	if err != nil {
		return total, inserted, err
	}

	pzClient, err := armprivatedns.NewPrivateZonesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armprivatedns:NewPrivateZonesClient: %w", err)
	}
	type privZoneRef struct {
		rg, name, discoID string
	}
	var privZones []privZoneRef
	pt, pi, err := azSimpleScan(ctx, "armprivatedns:PrivateZones.List", TypeDNSPrivateZone, sub, st, scanID,
		pzClient.NewListPager(nil),
		func(p armprivatedns.PrivateZonesClientListResponse) []*armprivatedns.PrivateZone { return p.Value },
		func(z *armprivatedns.PrivateZone) azTrackedBase {
			b := azTrackedBase{id: sv(z.ID), name: sv(z.Name), location: sv(z.Location), tags: z.Tags, full: z}
			if rg := rgNameFromID(b.id); rg != "" {
				privZones = append(privZones, privZoneRef{
					rg: rg, name: b.name,
					discoID: store.ResourceID("azure", sub.ID, TypeDNSPrivateZone, b.id),
				})
			}
			return b
		})
	total += pt
	inserted += pi
	if err != nil {
		return total, inserted, err
	}

	if len(privZones) == 0 {
		return total, inserted, nil
	}

	// Per-private-zone fan-out for VirtualNetworkLinks. Each zone has at most
	// a handful of links so fanoutMed semantics fit; semaphore bounds
	// concurrent ARM calls per sub.
	linkClient, err := armprivatedns.NewVirtualNetworkLinksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armprivatedns:NewVirtualNetworkLinksClient: %w", err)
	}
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)
	type linkResult struct {
		zoneDiscoID string
		batch       []*store.Resource
		pairs       [][2]string
	}
	results := make([]linkResult, len(privZones))
	for i, pz := range privZones {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			pager := linkClient.NewListPager(pz.rg, pz.name, nil)
			var batch []*store.Resource
			var pairs [][2]string
			for pager.More() {
				page, err := pager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return err
				}
				for _, l := range page.Value {
					if l == nil || l.ID == nil {
						continue
					}
					name, loc := sv(l.Name), sv(l.Location)
					nativeID := sv(l.ID)
					batch = append(batch, &store.Resource{
						Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
						Type: TypeDNSPrivateZoneVNetLink, NativeID: nativeID,
						Name: &name, Region: &loc,
						TagsJSON: azTagsJSON(l.Tags), AttributesJSON: mustJSON(l),
						DiscoveredBy: scanID,
					})
					linkID := store.ResourceID("azure", sub.ID, TypeDNSPrivateZoneVNetLink, nativeID)
					pairs = append(pairs, [2]string{linkID, pz.discoID})
				}
			}
			results[i] = linkResult{zoneDiscoID: pz.discoID, batch: batch, pairs: pairs}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return total, inserted, fmt.Errorf("armprivatedns:VirtualNetworkLinks.List: %w", err)
	}
	for _, r := range results {
		if len(r.batch) == 0 {
			continue
		}
		n, err := st.UpsertResources(r.batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert vnet links: %w", err)
		}
		total += len(r.batch)
		inserted += n
		if len(r.pairs) > 0 {
			if err := st.BatchAddToHierarchyClosure(r.pairs); err != nil {
				return total, inserted, fmt.Errorf("closure vnet links: %w", err)
			}
		}
	}

	// Public-zone record-set fan-out. Single ListAllByDNSZone returns every
	// record type in the zone (paginator-native). SOA records are skipped —
	// auto-managed and offer no graph value. Each zone has at most a handful
	// of pages; concurrency bounded by maxConcurrentFanout.
	if len(pubZones) > 0 {
		rsClient, err := armdns.NewRecordSetsClient(sub.ID, cred, azClientOptions)
		if err != nil {
			return total, inserted, fmt.Errorf("armdns:NewRecordSetsClient: %w", err)
		}
		refs := make([]recordSetRef, len(pubZones))
		for i, z := range pubZones {
			refs[i] = recordSetRef{rg: z.rg, name: z.name, parentDiscoID: z.discoID}
		}
		pt, pi, err := dnsRecordSetFanout(ctx, st, sub, scanID,
			"armdns:RecordSets.ListAllByDNSZone", TypeDNSRecordSet, refs,
			func(rg, name string) recordSetPager {
				return &armdnsPager{p: rsClient.NewListAllByDNSZonePager(rg, name, nil)}
			})
		total += pt
		inserted += pi
		if err != nil {
			return total, inserted, err
		}
	}

	// Private-zone record-set fan-out. Same shape as public zones.
	if len(privZones) > 0 {
		prsClient, err := armprivatedns.NewRecordSetsClient(sub.ID, cred, azClientOptions)
		if err != nil {
			return total, inserted, fmt.Errorf("armprivatedns:NewRecordSetsClient: %w", err)
		}
		refs := make([]recordSetRef, len(privZones))
		for i, z := range privZones {
			refs[i] = recordSetRef{rg: z.rg, name: z.name, parentDiscoID: z.discoID}
		}
		pt, pi, err := dnsRecordSetFanout(ctx, st, sub, scanID,
			"armprivatedns:RecordSets.List", TypeDNSPrivateRecordSet, refs,
			func(rg, name string) recordSetPager {
				return &armprivatednsPager{p: prsClient.NewListPager(rg, name, nil)}
			})
		total += pt
		inserted += pi
		if err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
}

// recordSetRef holds the parent-zone fan-out key (rg + zone name) and the
// disco-ID of the parent zone for hierarchy pairs.
type recordSetRef struct {
	rg, name, parentDiscoID string
}

// recordSetPager is the minimal pager surface needed for both armdns + armprivatedns
// record-set list APIs. Each page returns []recordSetRow with NativeID +
// AttributesJSON + a synthetic Location (zone is global; record-sets carry no
// location of their own).
type recordSetPager interface {
	More() bool
	NextPage(ctx context.Context) ([]recordSetRow, error)
}

type recordSetRow struct {
	id, name, attrsJSON string
}

type armdnsPager struct {
	p armdnsListAllByDNSZonePager
}

type armdnsListAllByDNSZonePager interface {
	More() bool
	NextPage(ctx context.Context) (armdns.RecordSetsClientListAllByDNSZoneResponse, error)
}

func (a *armdnsPager) More() bool { return a.p.More() }
func (a *armdnsPager) NextPage(ctx context.Context) ([]recordSetRow, error) {
	page, err := a.p.NextPage(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]recordSetRow, 0, len(page.Value))
	for _, r := range page.Value {
		if r == nil || r.ID == nil {
			continue
		}
		// Skip SOA records — auto-managed, no graph value.
		if recordTypeFromID(sv(r.ID)) == "SOA" {
			continue
		}
		out = append(out, recordSetRow{id: sv(r.ID), name: sv(r.Name), attrsJSON: mustJSON(r)})
	}
	return out, nil
}

type armprivatednsPager struct {
	p armprivatednsListPager
}

type armprivatednsListPager interface {
	More() bool
	NextPage(ctx context.Context) (armprivatedns.RecordSetsClientListResponse, error)
}

func (a *armprivatednsPager) More() bool { return a.p.More() }
func (a *armprivatednsPager) NextPage(ctx context.Context) ([]recordSetRow, error) {
	page, err := a.p.NextPage(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]recordSetRow, 0, len(page.Value))
	for _, r := range page.Value {
		if r == nil || r.ID == nil {
			continue
		}
		if recordTypeFromID(sv(r.ID)) == "SOA" {
			continue
		}
		out = append(out, recordSetRow{id: sv(r.ID), name: sv(r.Name), attrsJSON: mustJSON(r)})
	}
	return out, nil
}

// dnsRecordSetFanout runs per-zone record-set listing concurrently bounded by
// maxConcurrentFanout, then upserts batches + hierarchy pairs (record-set →
// parent zone).
func dnsRecordSetFanout(ctx context.Context, st *store.Store, sub *subscription, scanID, action, rtype string, zones []recordSetRef, makePager func(rg, name string) recordSetPager) (total, inserted int, err error) {
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)
	type zoneResult struct {
		batch []*store.Resource
		pairs [][2]string
	}
	results := make([]zoneResult, len(zones))
	for i, z := range zones {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			pager := makePager(z.rg, z.name)
			var batch []*store.Resource
			var pairs [][2]string
			for pager.More() {
				rows, err := pager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return err
				}
				for _, row := range rows {
					name := row.name
					batch = append(batch, &store.Resource{
						Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
						Type: rtype, NativeID: row.id,
						Name:           &name,
						AttributesJSON: row.attrsJSON,
						DiscoveredBy:   scanID,
					})
					pairs = append(pairs, [2]string{store.ResourceID("azure", sub.ID, rtype, row.id), z.parentDiscoID})
				}
			}
			results[i] = zoneResult{batch: batch, pairs: pairs}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return total, inserted, fmt.Errorf("%s: %w", action, err)
	}
	for _, r := range results {
		if len(r.batch) == 0 {
			continue
		}
		n, err := st.UpsertResources(r.batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert %s: %w", action, err)
		}
		total += len(r.batch)
		inserted += n
		if len(r.pairs) > 0 {
			if err := st.BatchAddToHierarchyClosure(r.pairs); err != nil {
				return total, inserted, fmt.Errorf("closure %s: %w", action, err)
			}
		}
	}
	return total, inserted, nil
}

// recordTypeFromID returns the record-set type segment of an ARM record-set
// ID (e.g. ".../dnsZones/{zone}/A/{name}" → "A"). Returns "" if shape doesn't
// match.
func recordTypeFromID(id string) string {
	parts := splitARMSegments(id)
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

func splitARMSegments(id string) []string {
	out := make([]string, 0, 16)
	start := 0
	for i := 0; i < len(id); i++ {
		if id[i] == '/' {
			if i > start {
				out = append(out, id[start:i])
			}
			start = i + 1
		}
	}
	if start < len(id) {
		out = append(out, id[start:])
	}
	return out
}
