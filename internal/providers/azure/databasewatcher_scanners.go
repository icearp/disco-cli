package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databasewatcher/armdatabasewatcher"
)

func init() {
	registerService(serviceEntry{
		name: "azure:databasewatcher",
		fn:   scanDatabaseWatcher,
		emits: []coverage.TypeDecl{
			// resolveDatabaseWatcherRelationships wires the datastore (Kusto)
			// edge below.
			{Service: "microsoft.databasewatcher", DiscoType: TypeDatabaseWatcher},
		},
	})
}

// scanDatabaseWatcher discovers Azure Database Watcher resources.
func scanDatabaseWatcher(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
