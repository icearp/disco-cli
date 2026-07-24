package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storageactions/armstorageactions"
)

func init() {
	registerType(restype.Descriptor{Type: TypeStorageActionsTask, Service: "microsoft.storageactions", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.storageactions",
		fn:   scanStorageActions,
	})
}

// scanStorageActions discovers Azure Storage Actions storage tasks.
func scanStorageActions(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armstorageactions.NewStorageTasksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armstorageactions:NewStorageTasksClient: %w", err)
	}
	return azSimpleScan(ctx, "armstorageactions:StorageTasks.ListBySubscription", TypeStorageActionsTask, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armstorageactions.StorageTasksClientListBySubscriptionResponse) []*armstorageactions.StorageTask {
			return p.Value
		},
		func(t *armstorageactions.StorageTask) azTrackedBase {
			return azTrackedBase{id: sv(t.ID), name: sv(t.Name), location: sv(t.Location), tags: t.Tags, full: t}
		})
}
