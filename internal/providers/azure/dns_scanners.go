package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() { registerService(serviceEntry{name: "azure:dns", fn: scanDNS}) }

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
	zt, zi, err := azPageScan(ctx, "armdns:Zones.List", sub, st,
		zonesClient.NewListPager(nil),
		func(page armdns.ZonesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, z := range page.Value {
				if z == nil || z.ID == nil {
					continue
				}
				name, loc := sv(z.Name), sv(z.Location)
				nativeID := sv(z.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeDNSZone, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(z.Tags), AttributesJSON: mustJSON(z),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeDNSZone, nativeID))
				}
			}
			return batch, pairs
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
	pt, pi, err := azPageScan(ctx, "armprivatedns:PrivateZones.List", sub, st,
		pzClient.NewListPager(nil),
		func(page armprivatedns.PrivateZonesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, z := range page.Value {
				if z == nil || z.ID == nil {
					continue
				}
				name, loc := sv(z.Name), sv(z.Location)
				nativeID := sv(z.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeDNSPrivateZone, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(z.Tags), AttributesJSON: mustJSON(z),
					DiscoveredBy: scanID,
				})
				rg := rgNameFromID(nativeID)
				if rg != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeDNSPrivateZone, nativeID))
					privZones = append(privZones, privZoneRef{
						rg: rg, name: name,
						discoID: store.ResourceID("azure", sub.ID, TypeDNSPrivateZone, nativeID),
					})
				}
			}
			return batch, pairs
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
	return total, inserted, nil
}
