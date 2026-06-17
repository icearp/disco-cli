package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/edgeorder/armedgeorder"
)

func init() {
	registerService(serviceEntry{
		name: "azure:edgeorder",
		fn:   scanEdgeOrder,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.edgeorder", DiscoType: TypeEdgeOrderItem, Leaf: true},
		},
	})
}

// scanEdgeOrder discovers Azure Edge Order items (hardware orders).
func scanEdgeOrder(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armedgeorder.NewManagementClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armedgeorder:NewManagementClient: %w", err)
	}
	return azSimpleScan(ctx, "armedgeorder:Management.ListOrderItemsAtSubscriptionLevel", TypeEdgeOrderItem, sub, st, scanID,
		client.NewListOrderItemsAtSubscriptionLevelPager(nil),
		func(p armedgeorder.ManagementClientListOrderItemsAtSubscriptionLevelResponse) []*armedgeorder.OrderItemResource {
			return p.Value
		},
		func(o *armedgeorder.OrderItemResource) azTrackedBase {
			return azTrackedBase{id: sv(o.ID), name: sv(o.Name), location: sv(o.Location), tags: o.Tags, full: o}
		})
}
