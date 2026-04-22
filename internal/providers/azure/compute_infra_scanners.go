package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

func scanAvailabilitySets(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewAvailabilitySetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewAvailabilitySetsClient: %w", err)
	}
	return azPageScan(ctx, "armcompute:AvailabilitySets.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armcompute.AvailabilitySetsClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, a := range page.Value {
				if a.ID == nil {
					continue
				}
				name, loc := sv(a.Name), sv(a.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeComputeAvailabilitySet, NativeID: sv(a.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(a.Tags), AttributesJSON: mustJSON(a),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeComputeAvailabilitySet, sv(a.ID)))
			}
			return batch, pairs
		})
}

func scanSSHPublicKeys(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewSSHPublicKeysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewSSHPublicKeysClient: %w", err)
	}
	return azPageScan(ctx, "armcompute:SSHPublicKeys.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armcompute.SSHPublicKeysClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, k := range page.Value {
				if k.ID == nil {
					continue
				}
				name, loc := sv(k.Name), sv(k.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeComputeSSHPublicKey, NativeID: sv(k.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(k.Tags), AttributesJSON: mustJSON(k),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeComputeSSHPublicKey, sv(k.ID)))
			}
			return batch, pairs
		})
}

func scanProximityPlacementGroups(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewProximityPlacementGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewProximityPlacementGroupsClient: %w", err)
	}
	return azPageScan(ctx, "armcompute:ProximityPlacementGroups.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armcompute.ProximityPlacementGroupsClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, p := range page.Value {
				if p.ID == nil {
					continue
				}
				name, loc := sv(p.Name), sv(p.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeComputeProximityPlacementGroup, NativeID: sv(p.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(p.Tags), AttributesJSON: mustJSON(p),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeComputeProximityPlacementGroup, sv(p.ID)))
			}
			return batch, pairs
		})
}

func scanComputeImages(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewImagesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewImagesClient: %w", err)
	}
	return azPageScan(ctx, "armcompute:Images.List", sub, st,
		client.NewListPager(nil),
		func(page armcompute.ImagesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, img := range page.Value {
				if img.ID == nil {
					continue
				}
				name, loc := sv(img.Name), sv(img.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeComputeImage, NativeID: sv(img.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(img.Tags), AttributesJSON: mustJSON(img),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeComputeImage, sv(img.ID)))
			}
			return batch, pairs
		})
}

func scanRestorePointCollections(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewRestorePointCollectionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewRestorePointCollectionsClient: %w", err)
	}
	return azPageScan(ctx, "armcompute:RestorePointCollections.ListAll", sub, st,
		client.NewListAllPager(nil),
		func(page armcompute.RestorePointCollectionsClientListAllResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, rpc := range page.Value {
				if rpc.ID == nil {
					continue
				}
				name, loc := sv(rpc.Name), sv(rpc.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeComputeRestorePointCollection, NativeID: sv(rpc.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(rpc.Tags), AttributesJSON: mustJSON(rpc),
					DiscoveredBy: scanID,
				})
				pairs = append(pairs, rgHierarchyPair(sub, TypeComputeRestorePointCollection, sv(rpc.ID)))
			}
			return batch, pairs
		})
}
