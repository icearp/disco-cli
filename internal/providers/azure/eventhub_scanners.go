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
	return azPageScan(ctx, "armeventhub:Namespaces.List", sub, st,
		client.NewListPager(nil),
		func(page armeventhub.NamespacesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, r := range page.Value {
				if r == nil || r.ID == nil {
					continue
				}
				name, loc := sv(r.Name), sv(r.Location)
				nativeID := sv(r.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeEventHubNamespace, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(r.Tags), AttributesJSON: mustJSON(r),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeEventHubNamespace, nativeID))
				}
			}
			return batch, pairs
		})
}
