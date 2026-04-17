package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

func scanDisks(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDisksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDisksClient: %w", err)
	}
	return azPageScan(ctx, "armcompute:Disks.List", sub, st,
		client.NewListPager(nil),
		func(page armcompute.DisksClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, d := range page.Value {
				if d.ID == nil {
					continue
				}
				name, loc := sv(d.Name), sv(d.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeComputeManagedDisk, NativeID: sv(d.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(d.Tags), AttributesJSON: mustJSON(d),
					DiscoveredBy: scanID,
				})
			}
			return batch, nil
		})
}

func scanSnapshots(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewSnapshotsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewSnapshotsClient: %w", err)
	}
	return azPageScan(ctx, "armcompute:Snapshots.List", sub, st,
		client.NewListPager(nil),
		func(page armcompute.SnapshotsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, s := range page.Value {
				if s.ID == nil {
					continue
				}
				name, loc := sv(s.Name), sv(s.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeComputeSnapshot, NativeID: sv(s.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(s.Tags), AttributesJSON: mustJSON(s),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeComputeSnapshot, sv(s.ID)))
			}
			return batch, pairs
		})
}

func scanDiskEncryptionSets(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDiskEncryptionSetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDiskEncryptionSetsClient: %w", err)
	}
	return azPageScan(ctx, "armcompute:DiskEncryptionSets.List", sub, st,
		client.NewListPager(nil),
		func(page armcompute.DiskEncryptionSetsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, des := range page.Value {
				if des.ID == nil {
					continue
				}
				name, loc := sv(des.Name), sv(des.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeComputeDiskEncryptionSet, NativeID: sv(des.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(des.Tags), AttributesJSON: mustJSON(des),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeComputeDiskEncryptionSet, sv(des.ID)))
			}
			return batch, pairs
		})
}

func scanDiskAccesses(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDiskAccessesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDiskAccessesClient: %w", err)
	}
	return azPageScan(ctx, "armcompute:DiskAccesses.List", sub, st,
		client.NewListPager(nil),
		func(page armcompute.DiskAccessesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, da := range page.Value {
				if da.ID == nil {
					continue
				}
				name, loc := sv(da.Name), sv(da.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeComputeDiskAccess, NativeID: sv(da.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(da.Tags), AttributesJSON: mustJSON(da),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeComputeDiskAccess, sv(da.ID)))
			}
			return batch, pairs
		})
}
