package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storageactions/armstorageactions"
)

func init() {
	registerType(restype.Descriptor{Type: TypeStorageStorageAccount, Service: "microsoft.storage"})
	registerType(restype.Descriptor{Type: TypeStorageStorageTask, Service: "microsoft.storage", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.storage",
		fn:   scanStorage,
	})
}

// scanStorage discovers Azure storage accounts and storage tasks. Storage tasks
// live under microsoft.storage but ship via the separate armstorageactions SDK module.
func scanStorage(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			client, err := armstorage.NewAccountsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armstorage:NewAccountsClient: %w", err)
			}
			return azSimpleScan(ctx, "armstorage:Accounts.List", TypeStorageStorageAccount, sub, st, scanID,
				client.NewListPager(nil),
				func(p armstorage.AccountsClientListResponse) []*armstorage.Account { return p.Value },
				func(a *armstorage.Account) azTrackedBase {
					return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
				})
		},
		func() (int, int, error) {
			taskClient, err := armstorageactions.NewStorageTasksClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armstorageactions:NewStorageTasksClient: %w", err)
			}
			return azSimpleScan(ctx, "armstorageactions:StorageTasks.ListBySubscription", TypeStorageStorageTask, sub, st, scanID,
				taskClient.NewListBySubscriptionPager(nil),
				func(p armstorageactions.StorageTasksClientListBySubscriptionResponse) []*armstorageactions.StorageTask {
					return p.Value
				},
				func(t *armstorageactions.StorageTask) azTrackedBase {
					return azTrackedBase{id: sv(t.ID), name: sv(t.Name), location: sv(t.Location), tags: t.Tags, full: t}
				})
		},
	)
}
