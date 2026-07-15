package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func init() {
	// Compute emits are declared per category file via registerExtraEmits
	// (compute_vms, compute_vmss, compute_disks, compute_galleries,
	// compute_dedicated, compute_infra, compute_cloudservices); scanCompute
	// itself upserts nothing.
	registerService(serviceEntry{name: "azure:microsoft.compute", fn: scanCompute})
}

// scanCompute discovers Azure Compute resources across all sub-groups: VMs, disks,
// images, VMSS, galleries, dedicated infrastructure, and cloud services.
// Phase 1 runs all types in parallel; phase 2 scans VM extensions (needs phase-1 VMs in store).
func scanCompute(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase 1: all subscription-scoped types concurrently via azRunPhases.
	// Unlike errgroup.WithContext, it never cancels siblings on a phase error —
	// a transient failure in one type (e.g. snapshots) must not wipe the others
	// ("errors never abort scan").
	total, inserted, err = azRunPhases(
		func() (int, int, error) { return scanVMs(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanDisks(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanAvailabilitySets(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanSSHPublicKeys(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanProximityPlacementGroups(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanComputeImages(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanSnapshots(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanDiskEncryptionSets(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanDiskAccesses(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanRestorePointCollections(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanVMSS(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanGalleries(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanDedicated(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanCloudServices(ctx, sub, cred, st, scanID) },
	)

	// Phase 2: VM extensions require VMs already in the store, so run after
	// phase 1. A phase-1 error is preserved but does not skip phase 2 — VMs may
	// have scanned fine even if a sibling type failed.
	t, n, e := scanVMExtensions(ctx, sub, cred, st, scanID)
	total += t
	inserted += n
	if err == nil {
		err = e
	}
	return total, inserted, err
}

// rgHierarchyPair computes the hierarchy closure pair (resourceID → rgID) for the
// resource at nativeID.
func rgHierarchyPair(sub *subscription, rtype, nativeID string) [2]string {
	rgName := rgFromID(nativeID)
	rgID := store.ResourceID("azure", sub.ID,
		fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", sub.ID, rgName))

	return [2]string{store.ResourceID("azure", sub.ID, nativeID), rgID}
}
