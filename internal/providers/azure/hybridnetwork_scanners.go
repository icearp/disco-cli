package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridnetwork/armhybridnetwork"
)

func init() {
	registerService(serviceEntry{
		name: "azure:hybridnetwork",
		fn:   scanHybridNetwork,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.hybridnetwork", DiscoType: TypeHybridNetworkFunction, Leaf: true},
		},
	})
}

// scanHybridNetwork discovers Azure Operator Service Manager network functions.
func scanHybridNetwork(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhybridnetwork.NewNetworkFunctionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhybridnetwork:NewNetworkFunctionsClient: %w", err)
	}
	return azSimpleScan(ctx, "armhybridnetwork:NetworkFunctions.ListBySubscription", TypeHybridNetworkFunction, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armhybridnetwork.NetworkFunctionsClientListBySubscriptionResponse) []*armhybridnetwork.NetworkFunction {
			return p.Value
		},
		func(f *armhybridnetwork.NetworkFunction) azTrackedBase {
			return azTrackedBase{id: sv(f.ID), name: sv(f.Name), location: sv(f.Location), tags: f.Tags, full: f}
		})
}
