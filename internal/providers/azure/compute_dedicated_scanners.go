package azure

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// hostGroupEntry holds the identifying fields of a dedicated host group for child scans.
type hostGroupEntry struct {
	rg, name, nativeID, discoID string
}

// crgEntry holds the identifying fields of a capacity reservation group for child scans.
type crgEntry struct {
	rg, name, nativeID, discoID string
}

// scanDedicated discovers Azure dedicated infrastructure: host groups, dedicated hosts,
// capacity reservation groups, and capacity reservations.
// Both top-level resource chains are scanned in parallel.
func scanDedicated(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		mu      sync.Mutex
		hgErrs  = make(chan error, 1)
		crgErrs = make(chan error, 1)
	)
	addTotals := func(t, n int) {
		mu.Lock()
		total += t
		inserted += n
		mu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		t, n, e := scanHostGroupChain(ctx, sub, cred, st, scanID)
		addTotals(t, n)
		if e != nil {
			hgErrs <- e
		}
	}()

	go func() {
		defer wg.Done()
		t, n, e := scanCRGChain(ctx, sub, cred, st, scanID)
		addTotals(t, n)
		if e != nil {
			crgErrs <- e
		}
	}()

	wg.Wait()

	select {
	case err := <-hgErrs:
		return 0, 0, err
	default:
	}
	select {
	case err := <-crgErrs:
		return 0, 0, err
	default:
	}
	return total, inserted, nil
}

// scanHostGroupChain scans host groups then fans out dedicated host scans per group.
func scanHostGroupChain(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	hgClient, err := armcompute.NewDedicatedHostGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDedicatedHostGroupsClient: %w", err)
	}

	var (
		hgBatch   []*store.Resource
		hgPairs   [][2]string
		hgEntries []hostGroupEntry
	)
	pager := hgClient.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:HostGroups.ListBySubscription", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:HostGroups.ListBySubscription: %w", err)
		}
		for _, hg := range page.Value {
			if hg.ID == nil {
				continue
			}
			name := sv(hg.Name)
			location := sv(hg.Location)
			nativeID := sv(hg.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeHostGroup,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(hg),
				DiscoveredBy:   scanID,
			}
			if hg.Tags != nil {
				s := mustJSON(hg.Tags)
				r.TagsJSON = &s
			}
			discoID := store.ResourceID("azure", sub.ID, TypeComputeHostGroup, nativeID)
			hgBatch = append(hgBatch, r)
			hgPairs = append(hgPairs, rgHierarchyPair(sub, TypeComputeHostGroup, nativeID))
			hgEntries = append(hgEntries, hostGroupEntry{
				rg:       rgNameFromID(nativeID),
				name:     name,
				nativeID: nativeID,
				discoID:  discoID,
			})
		}
	}
	if len(hgBatch) > 0 {
		n, err := st.UpsertResources(hgBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure host groups: %w", err)
		}
		total += len(hgBatch)
		inserted += n
		if err := st.BatchAddToHierarchyClosure(hgPairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure host groups: %w", err)
		}
	}
	if len(hgEntries) == 0 {
		return total, inserted, nil
	}

	// Fan out dedicated host scans per host group.
	hostClient, err := armcompute.NewDedicatedHostsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDedicatedHostsClient: %w", err)
	}

	var (
		mu                sync.Mutex
		hTotal, hInserted int
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gCtx := errgroup.WithContext(ctx)
	for _, hg := range hgEntries {
		entry := hg
		g.Go(func() error {
			if err := sem.Acquire(gCtx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			t, n, e := scanDedicatedHosts(gCtx, sub, hostClient, st, scanID, entry)
			if e != nil {
				return e
			}
			mu.Lock()
			hTotal += t
			hInserted += n
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return total + hTotal, inserted + hInserted, nil
}

func scanDedicatedHosts(ctx context.Context, sub *subscription, client *armcompute.DedicatedHostsClient, st *store.Store, scanID string, hg hostGroupEntry) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListByHostGroupPager(hg.rg, hg.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:DedicatedHosts.ListByHostGroup", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:DedicatedHosts.ListByHostGroup %s/%s: %w", hg.rg, hg.name, err)
		}
		for _, h := range page.Value {
			if h.ID == nil {
				continue
			}
			name := sv(h.Name)
			location := sv(h.Location)
			nativeID := sv(h.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeDedicatedHost,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(h),
				DiscoveredBy:   scanID,
			}
			if h.Tags != nil {
				s := mustJSON(h.Tags)
				r.TagsJSON = &s
			}
			hostID := store.ResourceID("azure", sub.ID, TypeComputeDedicatedHost, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{hostID, hg.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure dedicated hosts %s: %w", hg.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure dedicated hosts %s: %w", hg.name, err)
		}
	}
	return total, inserted, nil
}

// scanCRGChain scans capacity reservation groups then fans out capacity reservation scans.
func scanCRGChain(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	crgClient, err := armcompute.NewCapacityReservationGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewCapacityReservationGroupsClient: %w", err)
	}

	var (
		crgBatch   []*store.Resource
		crgPairs   [][2]string
		crgEntries []crgEntry
	)
	pager := crgClient.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:CapacityReservationGroups.ListBySubscription", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:CapacityReservationGroups.ListBySubscription: %w", err)
		}
		for _, crg := range page.Value {
			if crg.ID == nil {
				continue
			}
			name := sv(crg.Name)
			location := sv(crg.Location)
			nativeID := sv(crg.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeCapacityReservationGroup,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(crg),
				DiscoveredBy:   scanID,
			}
			if crg.Tags != nil {
				s := mustJSON(crg.Tags)
				r.TagsJSON = &s
			}
			discoID := store.ResourceID("azure", sub.ID, TypeComputeCapacityReservationGroup, nativeID)
			crgBatch = append(crgBatch, r)
			crgPairs = append(crgPairs, rgHierarchyPair(sub, TypeComputeCapacityReservationGroup, nativeID))
			crgEntries = append(crgEntries, crgEntry{
				rg:       rgNameFromID(nativeID),
				name:     name,
				nativeID: nativeID,
				discoID:  discoID,
			})
		}
	}
	if len(crgBatch) > 0 {
		n, err := st.UpsertResources(crgBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure capacity reservation groups: %w", err)
		}
		total += len(crgBatch)
		inserted += n
		if err := st.BatchAddToHierarchyClosure(crgPairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure capacity reservation groups: %w", err)
		}
	}
	if len(crgEntries) == 0 {
		return total, inserted, nil
	}

	// Fan out capacity reservation scans per CRG.
	crClient, err := armcompute.NewCapacityReservationsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewCapacityReservationsClient: %w", err)
	}

	var (
		mu                sync.Mutex
		cTotal, cInserted int
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gCtx := errgroup.WithContext(ctx)
	for _, crg := range crgEntries {
		entry := crg
		g.Go(func() error {
			if err := sem.Acquire(gCtx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			t, n, e := scanCapacityReservations(gCtx, sub, crClient, st, scanID, entry)
			if e != nil {
				return e
			}
			mu.Lock()
			cTotal += t
			cInserted += n
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return total + cTotal, inserted + cInserted, nil
}

func scanCapacityReservations(ctx context.Context, sub *subscription, client *armcompute.CapacityReservationsClient, st *store.Store, scanID string, crg crgEntry) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListByCapacityReservationGroupPager(crg.rg, crg.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:CapacityReservations.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:CapacityReservations.List %s/%s: %w", crg.rg, crg.name, err)
		}
		for _, cr := range page.Value {
			if cr.ID == nil {
				continue
			}
			name := sv(cr.Name)
			location := sv(cr.Location)
			nativeID := sv(cr.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeCapacityReservation,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(cr),
				DiscoveredBy:   scanID,
			}
			if cr.Tags != nil {
				s := mustJSON(cr.Tags)
				r.TagsJSON = &s
			}
			crID := store.ResourceID("azure", sub.ID, TypeComputeCapacityReservation, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{crID, crg.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure capacity reservations %s: %w", crg.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure capacity reservations %s: %w", crg.name, err)
		}
	}
	return total, inserted, nil
}
