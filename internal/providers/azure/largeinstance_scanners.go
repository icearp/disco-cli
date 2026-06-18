package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/largeinstance/armlargeinstance"
)

func init() {
	registerService(serviceEntry{
		name: "azure:azurelargeinstance",
		fn:   scanLargeInstance,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.azurelargeinstance", DiscoType: TypeLargeInstance, Leaf: true},
			{Service: "microsoft.azurelargeinstance", DiscoType: TypeLargeInstanceStorage, Leaf: true},
		},
	})
}

// scanLargeInstance discovers Azure Large Instances and large storage instances.
func scanLargeInstance(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	inst, err := armlargeinstance.NewAzureLargeInstanceClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armlargeinstance:NewAzureLargeInstanceClient: %w", err)
	}
	storage, err := armlargeinstance.NewAzureLargeStorageInstanceClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armlargeinstance:NewAzureLargeStorageInstanceClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armlargeinstance:AzureLargeInstance.ListBySubscription", TypeLargeInstance, sub, st, scanID,
				inst.NewListBySubscriptionPager(nil),
				func(p armlargeinstance.AzureLargeInstanceClientListBySubscriptionResponse) []*armlargeinstance.AzureLargeInstance {
					return p.Value
				},
				func(i *armlargeinstance.AzureLargeInstance) azTrackedBase {
					return azTrackedBase{id: sv(i.ID), name: sv(i.Name), location: sv(i.Location), tags: i.Tags, full: i}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armlargeinstance:AzureLargeStorageInstance.ListBySubscription", TypeLargeInstanceStorage, sub, st, scanID,
				storage.NewListBySubscriptionPager(nil),
				func(p armlargeinstance.AzureLargeStorageInstanceClientListBySubscriptionResponse) []*armlargeinstance.AzureLargeStorageInstance {
					return p.Value
				},
				func(r *armlargeinstance.AzureLargeStorageInstance) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
