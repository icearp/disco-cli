package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storagemover/armstoragemover"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.storagemover",
		fn:   scanStorageMover,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; storage/NFS endpoints
			// are child resources (not scanned), so the top-level mover ships
			// scanner-only.
			{Service: "microsoft.storagemover", DiscoType: TypeStorageMover, Leaf: true},
		},
	})
}

// scanStorageMover discovers Azure Storage Mover resources.
func scanStorageMover(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armstoragemover.NewStorageMoversClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armstoragemover:NewStorageMoversClient: %w", err)
	}
	return azSimpleScan(ctx, "armstoragemover:StorageMovers.ListBySubscription", TypeStorageMover, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armstoragemover.StorageMoversClientListBySubscriptionResponse) []*armstoragemover.StorageMover {
			return p.Value
		},
		func(m *armstoragemover.StorageMover) azTrackedBase {
			return azTrackedBase{id: sv(m.ID), name: sv(m.Name), location: sv(m.Location), tags: m.Tags, full: m}
		})
}
