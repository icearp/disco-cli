package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridconnectivity/armhybridconnectivity"
)

func init() {
	registerType(restype.Descriptor{Type: TypeHybridConnectivityPublicCloud, Service: "microsoft.hybridconnectivity", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.hybridconnectivity",
		fn:   scanHybridConnectivity,
	})
}

// scanHybridConnectivity discovers Arc public-cloud connectors (multicloud
// onboarding roots), sub-wide via armhybridconnectivity.
func scanHybridConnectivity(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhybridconnectivity.NewPublicCloudConnectorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhybridconnectivity:NewPublicCloudConnectorsClient: %w", err)
	}
	return azSimpleScan(ctx, "armhybridconnectivity:PublicCloudConnectors.ListBySubscription", TypeHybridConnectivityPublicCloud, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armhybridconnectivity.PublicCloudConnectorsClientListBySubscriptionResponse) []*armhybridconnectivity.PublicCloudConnector {
			return p.Value
		},
		func(c *armhybridconnectivity.PublicCloudConnector) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
