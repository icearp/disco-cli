package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/servicebus/armservicebus"
)

func init() { registerService(serviceEntry{name: "azure:servicebus", fn: scanServiceBus}) }

// scanServiceBus discovers Azure Service Bus namespaces. Queues, topics,
// subscriptions, rules, authorization rules, network rule sets, DR configs,
// and migration configs deferred — sub-resources with narrow cross-service
// edge value beyond the namespace row.
func scanServiceBus(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armservicebus.NewNamespacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armservicebus:NewNamespacesClient: %w", err)
	}
	return azPageScan(ctx, "armservicebus:Namespaces.List", sub, st,
		client.NewListPager(nil),
		func(page armservicebus.NamespacesClientListResponse) ([]*store.Resource, [][2]string) {
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
					Type: TypeServiceBusNamespace, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(r.Tags), AttributesJSON: mustJSON(r),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeServiceBusNamespace, nativeID))
				}
			}
			return batch, pairs
		})
}
