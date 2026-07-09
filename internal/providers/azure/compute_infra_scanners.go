package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

func init() {
	registerType(restype.Descriptor{Type: TypeComputeAvailabilitySet, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeImage, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeProximityPlacementGroup, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeRestorePointCollection, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeSSHPublicKey, Service: "microsoft.compute"})
}

func scanAvailabilitySets(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewAvailabilitySetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewAvailabilitySetsClient: %w", err)
	}
	return azSimpleScan(ctx, "armcompute:AvailabilitySets.ListBySubscription", TypeComputeAvailabilitySet, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armcompute.AvailabilitySetsClientListBySubscriptionResponse) []*armcompute.AvailabilitySet {
			return p.Value
		},
		func(a *armcompute.AvailabilitySet) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}

func scanSSHPublicKeys(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewSSHPublicKeysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewSSHPublicKeysClient: %w", err)
	}
	return azSimpleScan(ctx, "armcompute:SSHPublicKeys.ListBySubscription", TypeComputeSSHPublicKey, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armcompute.SSHPublicKeysClientListBySubscriptionResponse) []*armcompute.SSHPublicKeyResource {
			return p.Value
		},
		func(k *armcompute.SSHPublicKeyResource) azTrackedBase {
			return azTrackedBase{id: sv(k.ID), name: sv(k.Name), location: sv(k.Location), tags: k.Tags, full: k}
		})
}

func scanProximityPlacementGroups(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewProximityPlacementGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewProximityPlacementGroupsClient: %w", err)
	}
	return azSimpleScan(ctx, "armcompute:ProximityPlacementGroups.ListBySubscription", TypeComputeProximityPlacementGroup, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armcompute.ProximityPlacementGroupsClientListBySubscriptionResponse) []*armcompute.ProximityPlacementGroup {
			return p.Value
		},
		func(p *armcompute.ProximityPlacementGroup) azTrackedBase {
			return azTrackedBase{id: sv(p.ID), name: sv(p.Name), location: sv(p.Location), tags: p.Tags, full: p}
		})
}

func scanComputeImages(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewImagesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewImagesClient: %w", err)
	}
	return azSimpleScan(ctx, "armcompute:Images.List", TypeComputeImage, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcompute.ImagesClientListResponse) []*armcompute.Image { return p.Value },
		func(i *armcompute.Image) azTrackedBase {
			return azTrackedBase{id: sv(i.ID), name: sv(i.Name), location: sv(i.Location), tags: i.Tags, full: i}
		})
}

func scanRestorePointCollections(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewRestorePointCollectionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewRestorePointCollectionsClient: %w", err)
	}
	return azSimpleScan(ctx, "armcompute:RestorePointCollections.ListAll", TypeComputeRestorePointCollection, sub, st, scanID,
		client.NewListAllPager(nil),
		func(p armcompute.RestorePointCollectionsClientListAllResponse) []*armcompute.RestorePointCollection {
			return p.Value
		},
		func(r *armcompute.RestorePointCollection) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
