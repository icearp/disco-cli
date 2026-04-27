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
	return azSimpleScan(ctx, "armservicebus:Namespaces.List", TypeServiceBusNamespace, sub, st, scanID,
		client.NewListPager(nil),
		func(p armservicebus.NamespacesClientListResponse) []*armservicebus.SBNamespace { return p.Value },
		func(r *armservicebus.SBNamespace) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
