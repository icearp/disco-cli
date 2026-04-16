package azure

import (
	"context"
	"fmt"
	"sync"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "azure:compute", fn: scanCompute}) }

// scanCompute discovers Azure Compute resources across all sub-groups: VMs, disks,
// images, VMSS, galleries, dedicated infrastructure, and cloud services.
// Phase 1 runs all resource types in parallel.
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
		t, n, e := scanDedicated(gctx, sub, cred, st, scanID)
		addTotals(t, n)
		return e
	})
	g.Go(func() error {
		t, n, e := scanCloudServices(gctx, sub, cred, st, scanID)
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
