package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storagesync/armstoragesync"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.storagesync",
		fn:   scanStorageSync,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.storagesync", DiscoType: TypeStorageSyncService, Leaf: true},
		},
	})
}

// scanStorageSync discovers Azure File Sync (Storage Sync) services.
func scanStorageSync(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armstoragesync.NewServicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armstoragesync:NewServicesClient: %w", err)
	}
	return azSimpleScan(ctx, "armstoragesync:Services.ListBySubscription", TypeStorageSyncService, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armstoragesync.ServicesClientListBySubscriptionResponse) []*armstoragesync.Service {
			return p.Value
		},
		func(s *armstoragesync.Service) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
}
