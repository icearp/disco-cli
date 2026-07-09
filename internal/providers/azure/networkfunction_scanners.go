package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/networkfunction/armnetworkfunction"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNetworkFunctionTrafficCollector, Service: "microsoft.networkfunction", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.networkfunction",
		fn:   scanNetworkFunction,
	})
}

// scanNetworkFunction discovers Azure Traffic Collectors.
func scanNetworkFunction(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetworkfunction.NewAzureTrafficCollectorsBySubscriptionClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetworkfunction:NewAzureTrafficCollectorsBySubscriptionClient: %w", err)
	}
	return azSimpleScan(ctx, "armnetworkfunction:AzureTrafficCollectors.List", TypeNetworkFunctionTrafficCollector, sub, st, scanID,
		client.NewListPager(nil),
		func(p armnetworkfunction.AzureTrafficCollectorsBySubscriptionClientListResponse) []*armnetworkfunction.AzureTrafficCollector {
			return p.Value
		},
		func(c *armnetworkfunction.AzureTrafficCollector) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
