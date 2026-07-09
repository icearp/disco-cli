package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databasewatcher/armdatabasewatcher"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDatabaseWatcher, Service: "microsoft.databasewatcher"})
	registerService(serviceEntry{
		name: "azure:microsoft.databasewatcher",
		fn:   scanDatabaseWatcher,
	})
}

// scanDatabaseWatcher discovers Azure Database Watcher resources.
func scanDatabaseWatcher(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdatabasewatcher.NewWatchersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdatabasewatcher:NewWatchersClient: %w", err)
	}
	return azSimpleScan(ctx, "armdatabasewatcher:Watchers.ListBySubscription", TypeDatabaseWatcher, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdatabasewatcher.WatchersClientListBySubscriptionResponse) []*armdatabasewatcher.Watcher {
			return p.Value
		},
		func(w *armdatabasewatcher.Watcher) azTrackedBase {
			return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
		})
}
