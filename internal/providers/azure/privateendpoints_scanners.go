package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

func init() { registerService(serviceEntry{name: "azure:privateendpoints", fn: scanPrivateEndpoints}) }

// scanPrivateEndpoints discovers Azure Private Endpoints subscription-wide.
// Each PE binds a target resource (storage account, sql server, key vault,
// cosmos account, ACR, etc.) into a VNet subnet via private link, so PE rows
// drive a fan of `attached-to` edges to whatever target type the resolvers
// understand. Private DNS zones, zone-groups, and private link services
// deferred — separate sub-resources whose graph value is mostly subsumed by
// the PE → target edge.
func scanPrivateEndpoints(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetwork.NewPrivateEndpointsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewPrivateEndpointsClient: %w", err)
	}
	return azPageScan(ctx, "armnetwork:PrivateEndpoints.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armnetwork.PrivateEndpointsClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, pe := range page.Value {
				if pe == nil || pe.ID == nil {
					continue
				}
				name, loc := sv(pe.Name), sv(pe.Location)
				nativeID := sv(pe.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeNetworkPrivateEndpoint, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(pe.Tags), AttributesJSON: mustJSON(pe),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeNetworkPrivateEndpoint, nativeID))
				}
			}
			return batch, pairs
		})
}
