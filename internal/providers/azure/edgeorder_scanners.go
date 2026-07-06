package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/edgeorder/armedgeorder"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.edgeorder",
		fn:   scanEdgeOrder,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.edgeorder", DiscoType: TypeEdgeOrderItem, Leaf: true},
			{Service: "microsoft.edgeorder", DiscoType: TypeEdgeOrderAddress, Leaf: true},
			{Service: "microsoft.edgeorder", DiscoType: TypeEdgeOrderOrder, Leaf: true},
		},
	})
}

// scanEdgeOrder discovers Azure Edge Order items, addresses, and orders via
// the single ManagementClient's subscription-level list pagers; orders are
// proxy resources (no location/tags).
func scanEdgeOrder(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armedgeorder.NewManagementClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armedgeorder:NewManagementClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armedgeorder:Management.ListOrderItemsAtSubscriptionLevel", TypeEdgeOrderItem, sub, st, scanID,
				client.NewListOrderItemsAtSubscriptionLevelPager(nil),
				func(p armedgeorder.ManagementClientListOrderItemsAtSubscriptionLevelResponse) []*armedgeorder.OrderItemResource {
					return p.Value
				},
				func(o *armedgeorder.OrderItemResource) azTrackedBase {
					return azTrackedBase{id: sv(o.ID), name: sv(o.Name), location: sv(o.Location), tags: o.Tags, full: o}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armedgeorder:Management.ListAddressesAtSubscriptionLevel", TypeEdgeOrderAddress, sub, st, scanID,
				client.NewListAddressesAtSubscriptionLevelPager(nil),
				func(p armedgeorder.ManagementClientListAddressesAtSubscriptionLevelResponse) []*armedgeorder.AddressResource {
					return p.Value
				},
				func(r *armedgeorder.AddressResource) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armedgeorder:Management.ListOrderAtSubscriptionLevel", TypeEdgeOrderOrder, sub, st, scanID,
				client.NewListOrderAtSubscriptionLevelPager(nil),
				func(p armedgeorder.ManagementClientListOrderAtSubscriptionLevelResponse) []*armedgeorder.OrderResource {
					return p.Value
				},
				func(o *armedgeorder.OrderResource) azTrackedBase {
					return azTrackedBase{id: sv(o.ID), name: sv(o.Name), full: o}
				})
		},
	)
}
