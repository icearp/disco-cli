package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNetworkPrivateEndpoint, Service: "microsoft.network"})
}

// scanPrivateEndpoints discovers Azure Private Endpoints subscription-wide.
// Each PE binds a target resource (storage account, sql server, key vault,
// cosmos account, ACR, etc.) into a VNet subnet via private link, so PE rows
// drive `attached-to` edges to whatever target type the resolvers understand.
// Private DNS zones, zone-groups, and private link services deferred —
// separate sub-resources whose graph value is mostly subsumed by the
// PE → target edge.
func scanPrivateEndpoints(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetwork.NewPrivateEndpointsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewPrivateEndpointsClient: %w", err)
	}
	return azSimpleScan(ctx, "armnetwork:PrivateEndpoints.ListBySubscription", TypeNetworkPrivateEndpoint, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armnetwork.PrivateEndpointsClientListBySubscriptionResponse) []*armnetwork.PrivateEndpoint {
			return p.Value
		},
		func(pe *armnetwork.PrivateEndpoint) azTrackedBase {
			return azTrackedBase{id: sv(pe.ID), name: sv(pe.Name), location: sv(pe.Location), tags: pe.Tags, full: pe}
		})
}
