package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

func scanDisks(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDisksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDisksClient: %w", err)
	}
	return azSimpleScan(ctx, "armcompute:Disks.List", TypeComputeManagedDisk, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcompute.DisksClientListResponse) []*armcompute.Disk { return p.Value },
		func(d *armcompute.Disk) azTrackedBase {
			return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
		})
}

func scanSnapshots(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewSnapshotsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewSnapshotsClient: %w", err)
	}
	return azSimpleScan(ctx, "armcompute:Snapshots.List", TypeComputeSnapshot, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcompute.SnapshotsClientListResponse) []*armcompute.Snapshot { return p.Value },
		func(s *armcompute.Snapshot) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
}

func scanDiskEncryptionSets(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDiskEncryptionSetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDiskEncryptionSetsClient: %w", err)
	}
	return azSimpleScan(ctx, "armcompute:DiskEncryptionSets.List", TypeComputeDiskEncryptionSet, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcompute.DiskEncryptionSetsClientListResponse) []*armcompute.DiskEncryptionSet {
			return p.Value
		},
		func(d *armcompute.DiskEncryptionSet) azTrackedBase {
			return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
		})
}

func scanDiskAccesses(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDiskAccessesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDiskAccessesClient: %w", err)
	}
	return azSimpleScan(ctx, "armcompute:DiskAccesses.List", TypeComputeDiskAccess, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcompute.DiskAccessesClientListResponse) []*armcompute.DiskAccess { return p.Value },
		func(d *armcompute.DiskAccess) azTrackedBase {
			return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
		})
}
