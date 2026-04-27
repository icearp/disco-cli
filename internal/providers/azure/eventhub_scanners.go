package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub"
)

func init() { registerService(serviceEntry{name: "azure:eventhub", fn: scanEventHub}) }

// scanEventHub discovers Azure Event Hubs namespaces. Per-namespace event
// hubs (queues), consumer groups, authorization rules, schema groups,
// disaster-recovery configs, application groups, and clusters deferred —
// sub-resources whose graph value is mostly subsumed by the namespace
// row's CMEK + identity edges.
func scanEventHub(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armeventhub.NewNamespacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armeventhub:NewNamespacesClient: %w", err)
	}
	return azSimpleScan(ctx, "armeventhub:Namespaces.List", TypeEventHubNamespace, sub, st, scanID,
		client.NewListPager(nil),
		func(p armeventhub.NamespacesClientListResponse) []*armeventhub.EHNamespace { return p.Value },
		func(r *armeventhub.EHNamespace) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
