package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEventHubNamespace, Service: "microsoft.eventhub"})
	registerType(restype.Descriptor{Type: TypeEventHubCluster, Service: "microsoft.eventhub", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.eventhub",
		fn:   scanEventHub,
	})
}

// scanEventHub discovers Azure Event Hubs namespaces. Per-namespace event
// hubs (queues), consumer groups, authorization rules, schema groups,
// disaster-recovery configs, application groups, and clusters deferred —
// sub-resources whose graph value is mostly subsumed by the namespace
// row's CMEK + identity edges.
func scanEventHub(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
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
		},
		func() (int, int, error) {
			clClient, err := armeventhub.NewClustersClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armeventhub:NewClustersClient: %w", err)
			}
			return azSimpleScan(ctx, "armeventhub:Clusters.ListBySubscription", TypeEventHubCluster, sub, st, scanID,
				clClient.NewListBySubscriptionPager(nil),
				func(p armeventhub.ClustersClientListBySubscriptionResponse) []*armeventhub.Cluster { return p.Value },
				func(c *armeventhub.Cluster) azTrackedBase {
					return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
				})
		},
	)
}
