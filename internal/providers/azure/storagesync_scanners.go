package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storagesync/armstoragesync"
)

func init() {
	registerType(restype.Descriptor{Type: TypeStorageSyncService, Service: "microsoft.storagesync", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.storagesync",
		fn:   scanStorageSync,
	})
}

// scanStorageSync discovers Azure File Sync (Storage Sync) services.
func scanStorageSync(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
