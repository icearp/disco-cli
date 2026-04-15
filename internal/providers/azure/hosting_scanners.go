package azure

import (
	"context"
	"fmt"
	"sync"

	"codeburg.org/icearp/disco/internal/store"
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

// cloudServiceEntry holds the identifying fields of a cloud service for child scans.
type cloudServiceEntry struct {
	rg, name, nativeID, discoID string
}

// scanHosting discovers Azure hosting resources: host groups, dedicated hosts,
// capacity reservation groups, capacity reservations, cloud services,
// cloud service roles, and cloud service role instances.
// All top-level resource types are scanned in parallel; child scans follow each parent batch.
func scanHosting(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		mu            sync.Mutex
		hostGroupErrs = make(chan error, 1)
		crgErrs       = make(chan error, 1)
		cloudSvcErrs  = make(chan error, 1)
	)
	addTotals := func(t, n int) {
		mu.Lock()
		total += t
		inserted += n
		mu.Unlock()
	}

	// Run three independent host-type scan chains in parallel.
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		t, n, e := scanHostGroupChain(ctx, sub, cred, st, scanID)
		addTotals(t, n)
		if e != nil {
			hostGroupErrs <- e
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

	go func() {
		defer wg.Done()
		t, n, e := scanCloudServiceChain(ctx, sub, cred, st, scanID)
		addTotals(t, n)
		if e != nil {
			cloudSvcErrs <- e
		}
	}()

	wg.Wait()

	// Return the first error encountered, if any.
	select {
	case err := <-hostGroupErrs:
		return 0, 0, err
	default:
	}
	select {
	case err := <-crgErrs:
		return 0, 0, err
	default:
	}
	select {
	case err := <-cloudSvcErrs:
		return 0, 0, err
	default:
	}
	return total, inserted, nil
}

// scanHostGroupChain scans host groups then fans out dedicated host scans per group.
func scanHostGroupChain(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	hgClient, err := armcompute.NewDedicatedHostGroupsClient(sub.ID, cred, nil)
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
				return 0, 0, skipIfAccessDenied("armcompute:HostGroups.ListBySubscription", sub.ID, err)
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
	hostClient, err := armcompute.NewDedicatedHostsClient(sub.ID, cred, nil)
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
				return 0, 0, skipIfAccessDenied("armcompute:DedicatedHosts.ListByHostGroup", sub.ID, err)
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
	crgClient, err := armcompute.NewCapacityReservationGroupsClient(sub.ID, cred, nil)
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
				return 0, 0, skipIfAccessDenied("armcompute:CapacityReservationGroups.ListBySubscription", sub.ID, err)
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
	crClient, err := armcompute.NewCapacityReservationsClient(sub.ID, cred, nil)
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
				return 0, 0, skipIfAccessDenied("armcompute:CapacityReservations.List", sub.ID, err)
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

// scanCloudServiceChain scans cloud services then fans out role and role instance scans.
func scanCloudServiceChain(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	csClient, err := armcompute.NewCloudServicesClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewCloudServicesClient: %w", err)
	}

	var (
		csBatch   []*store.Resource
		csPairs   [][2]string
		csEntries []cloudServiceEntry
	)
	pager := csClient.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:CloudServices.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:CloudServices.ListAll: %w", err)
		}
		for _, cs := range page.Value {
			if cs.ID == nil {
				continue
			}
			name := sv(cs.Name)
			location := sv(cs.Location)
			nativeID := sv(cs.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeCloudService,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(cs),
				DiscoveredBy:   scanID,
			}
			if cs.Tags != nil {
				s := mustJSON(cs.Tags)
				r.TagsJSON = &s
			}
			discoID := store.ResourceID("azure", sub.ID, TypeComputeCloudService, nativeID)
			csBatch = append(csBatch, r)
			csPairs = append(csPairs, rgHierarchyPair(sub, TypeComputeCloudService, nativeID))
			csEntries = append(csEntries, cloudServiceEntry{
				rg:       rgNameFromID(nativeID),
				name:     name,
				nativeID: nativeID,
				discoID:  discoID,
			})
		}
	}
	if len(csBatch) > 0 {
		n, err := st.UpsertResources(csBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure cloud services: %w", err)
		}
		total += len(csBatch)
		inserted += n
		if err := st.BatchAddToHierarchyClosure(csPairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure cloud services: %w", err)
		}
	}
	if len(csEntries) == 0 {
		return total, inserted, nil
	}

	// Fan out role and role instance scans per cloud service.
	roleClient, err := armcompute.NewCloudServiceRolesClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewCloudServiceRolesClient: %w", err)
	}
	riClient, err := armcompute.NewCloudServiceRoleInstancesClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewCloudServiceRoleInstancesClient: %w", err)
	}

	var (
		mu                sync.Mutex
		cTotal, cInserted int
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gCtx := errgroup.WithContext(ctx)
	for _, cs := range csEntries {
		entry := cs
		g.Go(func() error {
			if err := sem.Acquire(gCtx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			rT, rN, rErr := scanCloudServiceRoles(gCtx, sub, roleClient, st, scanID, entry)
			if rErr != nil {
				return rErr
			}
			riT, riN, riErr := scanCloudServiceRoleInstances(gCtx, sub, riClient, st, scanID, entry)
			if riErr != nil {
				return riErr
			}
			mu.Lock()
			cTotal += rT + riT
			cInserted += rN + riN
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return total + cTotal, inserted + cInserted, nil
}

func scanCloudServiceRoles(ctx context.Context, sub *subscription, client *armcompute.CloudServiceRolesClient, st *store.Store, scanID string, cs cloudServiceEntry) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListPager(cs.rg, cs.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:CloudServiceRoles.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:CloudServiceRoles.List %s/%s: %w", cs.rg, cs.name, err)
		}
		for _, role := range page.Value {
			if role.ID == nil {
				continue
			}
			name := sv(role.Name)
			nativeID := sv(role.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeCloudServiceRole,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(role),
				DiscoveredBy:   scanID,
			}
			roleID := store.ResourceID("azure", sub.ID, TypeComputeCloudServiceRole, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{roleID, cs.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure cloud service roles %s: %w", cs.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure cloud service roles %s: %w", cs.name, err)
		}
	}
	return total, inserted, nil
}

func scanCloudServiceRoleInstances(ctx context.Context, sub *subscription, client *armcompute.CloudServiceRoleInstancesClient, st *store.Store, scanID string, cs cloudServiceEntry) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListPager(cs.rg, cs.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:CloudServiceRoleInstances.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:CloudServiceRoleInstances.List %s/%s: %w", cs.rg, cs.name, err)
		}
		for _, ri := range page.Value {
			if ri.ID == nil {
				continue
			}
			name := sv(ri.Name)
			nativeID := sv(ri.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeCloudServiceRoleInstance,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(ri),
				DiscoveredBy:   scanID,
			}
			riID := store.ResourceID("azure", sub.ID, TypeComputeCloudServiceRoleInstance, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{riID, cs.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure cloud service role instances %s: %w", cs.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure cloud service role instances %s: %w", cs.name, err)
		}
	}
	return total, inserted, nil
}
