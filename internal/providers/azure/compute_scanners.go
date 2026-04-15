package azure

import (
	"context"
	"fmt"
	"sync"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() { registerService(serviceEntry{name: "azure:compute", fn: scanCompute}) }

// scanCompute discovers Azure Compute resources: VMs, disks, availability sets,
// SSH public keys, proximity placement groups, images, snapshots, disk encryption
// sets, disk accesses, restore point collections, and VM extensions.
// Phase 1 runs all standalone resource types in parallel.
// Phase 2 scans VM extensions (depends on Phase 1 VMs being in the store).
func scanCompute(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	addTotals := func(t, n int) {
		mu.Lock()
		total += t
		inserted += n
		mu.Unlock()
	}

	// Phase 1: all subscription-scoped resource types in parallel.
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		t, n, e := scanVMs(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanDisks(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanAvailabilitySets(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanSSHPublicKeys(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanProximityPlacementGroups(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanComputeImages(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanSnapshots(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanDiskEncryptionSets(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanDiskAccesses(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanRestorePointCollections(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanVMSS(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanGalleries(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanHosting(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})

	if err := g.Wait(); err != nil {
		return 0, 0, err
	}

	// Phase 2: VM extensions require VMs to already be in the store.
	t, n, e := scanVMExtensions(ctx, sub, cred, st, scanID)
	if e != nil {
		return 0, 0, e
	}
	total += t
	inserted += n

	return total, inserted, nil
}

// rgHierarchyPair computes the hierarchy closure pair (resourceID → rgID) for a
// resource whose Azure ID is nativeID.
func rgHierarchyPair(sub *subscription, rtype, nativeID string) [2]string {
	rgName := rgFromID(nativeID)
	rgID := store.ResourceID("azure", sub.ID, TypeResourcesResourceGroup,
		fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", sub.ID, rgName))
	return [2]string{store.ResourceID("azure", sub.ID, rtype, nativeID), rgID}
}

func scanVMs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewVirtualMachinesClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewVirtualMachinesClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:VMs.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:VMs.ListAll: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, vm := range page.Value {
			if vm.ID == nil {
				continue
			}
			name := sv(vm.Name)
			location := sv(vm.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeVirtualMachine,
				NativeID:       sv(vm.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(vm),
				DiscoveredBy:   scanID,
			}
			if len(vm.Zones) > 0 {
				z := sv(vm.Zones[0])
				r.Zone = &z
			}
			if vm.Properties != nil {
				r.CreatedAt = tp(vm.Properties.TimeCreated)
			}
			if vm.Tags != nil {
				s := mustJSON(vm.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeVirtualMachine, sv(vm.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure VMs: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure VMs: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanDisks(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDisksClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDisksClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:Disks.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:Disks.List: %w", err)
		}
		var batch []*store.Resource
		for _, d := range page.Value {
			if d.ID == nil {
				continue
			}
			name := sv(d.Name)
			location := sv(d.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeManagedDisk,
				NativeID:       sv(d.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			}
			if d.Tags != nil {
				s := mustJSON(d.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure disks: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanAvailabilitySets(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewAvailabilitySetsClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewAvailabilitySetsClient: %w", err)
	}

	pager := client.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:AvailabilitySets.ListBySubscription", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:AvailabilitySets.ListBySubscription: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, a := range page.Value {
			if a.ID == nil {
				continue
			}
			name := sv(a.Name)
			location := sv(a.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeAvailabilitySet,
				NativeID:       sv(a.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			}
			if a.Tags != nil {
				s := mustJSON(a.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeAvailabilitySet, sv(a.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure availability sets: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure availability sets: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanSSHPublicKeys(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewSSHPublicKeysClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewSSHPublicKeysClient: %w", err)
	}

	pager := client.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:SSHPublicKeys.ListBySubscription", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:SSHPublicKeys.ListBySubscription: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, k := range page.Value {
			if k.ID == nil {
				continue
			}
			name := sv(k.Name)
			location := sv(k.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeSSHPublicKey,
				NativeID:       sv(k.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(k),
				DiscoveredBy:   scanID,
			}
			if k.Tags != nil {
				s := mustJSON(k.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeSSHPublicKey, sv(k.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure SSH public keys: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure SSH public keys: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanProximityPlacementGroups(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewProximityPlacementGroupsClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewProximityPlacementGroupsClient: %w", err)
	}

	pager := client.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:ProximityPlacementGroups.ListBySubscription", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:ProximityPlacementGroups.ListBySubscription: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, p := range page.Value {
			if p.ID == nil {
				continue
			}
			name := sv(p.Name)
			location := sv(p.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeProximityPlacementGroup,
				NativeID:       sv(p.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			}
			if p.Tags != nil {
				s := mustJSON(p.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeProximityPlacementGroup, sv(p.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure proximity placement groups: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure proximity placement groups: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanComputeImages(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewImagesClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewImagesClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:Images.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:Images.List: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, img := range page.Value {
			if img.ID == nil {
				continue
			}
			name := sv(img.Name)
			location := sv(img.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeImage,
				NativeID:       sv(img.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(img),
				DiscoveredBy:   scanID,
			}
			if img.Tags != nil {
				s := mustJSON(img.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeImage, sv(img.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure compute images: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure compute images: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanSnapshots(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewSnapshotsClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewSnapshotsClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:Snapshots.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:Snapshots.List: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, s := range page.Value {
			if s.ID == nil {
				continue
			}
			name := sv(s.Name)
			location := sv(s.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeSnapshot,
				NativeID:       sv(s.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
			if s.Tags != nil {
				tags := mustJSON(s.Tags)
				r.TagsJSON = &tags
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeSnapshot, sv(s.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure snapshots: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure snapshots: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanDiskEncryptionSets(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDiskEncryptionSetsClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDiskEncryptionSetsClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:DiskEncryptionSets.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:DiskEncryptionSets.List: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, des := range page.Value {
			if des.ID == nil {
				continue
			}
			name := sv(des.Name)
			location := sv(des.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeDiskEncryptionSet,
				NativeID:       sv(des.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(des),
				DiscoveredBy:   scanID,
			}
			if des.Tags != nil {
				s := mustJSON(des.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeDiskEncryptionSet, sv(des.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure disk encryption sets: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure disk encryption sets: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanDiskAccesses(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDiskAccessesClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDiskAccessesClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:DiskAccesses.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:DiskAccesses.List: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, da := range page.Value {
			if da.ID == nil {
				continue
			}
			name := sv(da.Name)
			location := sv(da.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeDiskAccess,
				NativeID:       sv(da.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(da),
				DiscoveredBy:   scanID,
			}
			if da.Tags != nil {
				s := mustJSON(da.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeDiskAccess, sv(da.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure disk accesses: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure disk accesses: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanRestorePointCollections(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewRestorePointCollectionsClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewRestorePointCollectionsClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:RestorePointCollections.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:RestorePointCollections.ListAll: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, rpc := range page.Value {
			if rpc.ID == nil {
				continue
			}
			name := sv(rpc.Name)
			location := sv(rpc.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeRestorePointCollection,
				NativeID:       sv(rpc.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(rpc),
				DiscoveredBy:   scanID,
			}
			if rpc.Tags != nil {
				s := mustJSON(rpc.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeRestorePointCollection, sv(rpc.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure restore point collections: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure restore point collections: %w", err)
			}
		}
	}
	return total, inserted, nil
}

// scanVMExtensions lists all extensions for every VM in the subscription.
// It fans out one API call per VM using errgroup, bounded by maxConcurrentFanout.
// Must be called after scanVMs has populated the store.
func scanVMExtensions(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewVirtualMachineExtensionsClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewVirtualMachineExtensionsClient: %w", err)
	}

	// Load all VMs for this subscription from the store.
	vms, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeVirtualMachine},
		Limit:     util.AllResources,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("list VMs for extension scan: %w", err)
	}
	if len(vms) == 0 {
		return 0, 0, nil
	}

	var (
		mu    sync.Mutex
		batch []*store.Resource
		pairs [][2]string
	)

	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)
	for _, vm := range vms {
		vmNativeID := vm.NativeID
		vmDiscoID := vm.ID // stable disco ID is the parent
		rgName := rgNameFromID(vmNativeID)
		vmName := nameFromID(vmNativeID)
		if rgName == "" || vmName == "" {
			continue
		}
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			resp, err := client.List(gctx, rgName, vmName, nil)
			if err != nil {
				if isAccessDenied(err) {
					return skipIfAccessDenied("armcompute:VMExtensions.List", sub.ID, err)
				}
				return fmt.Errorf("armcompute:VMExtensions.List %s/%s: %w", rgName, vmName, err)
			}

			var localBatch []*store.Resource
			var localPairs [][2]string
			for _, ext := range resp.Value {
				if ext.ID == nil {
					continue
				}
				name := sv(ext.Name)
				location := sv(ext.Location)
				r := &store.Resource{
					Provider:       "azure",
					AccountID:      sub.ID,
					AccountName:    &sub.Name,
					Type:           TypeComputeVMExtension,
					NativeID:       sv(ext.ID),
					Name:           &name,
					Region:         &location,
					AttributesJSON: mustJSON(ext),
					DiscoveredBy:   scanID,
				}
				localBatch = append(localBatch, r)
				extID := store.ResourceID("azure", sub.ID, TypeComputeVMExtension, sv(ext.ID))
				localPairs = append(localPairs, [2]string{extID, vmDiscoID})
			}
			if len(localBatch) > 0 {
				mu.Lock()
				batch = append(batch, localBatch...)
				pairs = append(pairs, localPairs...)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}

	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure VM extensions: %w", err)
		}
		total = len(batch)
		inserted = n
		if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure VM extensions: %w", err)
		}
	}
	return total, inserted, nil
}
