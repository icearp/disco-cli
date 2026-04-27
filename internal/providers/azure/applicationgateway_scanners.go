package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

func init() {
	registerService(serviceEntry{name: "azure:applicationgateway", fn: scanApplicationGateway})
}

// scanApplicationGateway discovers Azure Application Gateways subscription-wide.
// Web Application Firewall policies, private link configurations, listener +
// rewrite-rule sub-resources are deferred — covered by the gateway's
// AttributesJSON for inspection but not promoted to standalone resource rows.
func scanApplicationGateway(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetwork.NewApplicationGatewaysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewApplicationGatewaysClient: %w", err)
	}
	return azPageScan(ctx, "armnetwork:ApplicationGateways.ListAll", sub, st,
		client.NewListAllPager(nil),
		func(page armnetwork.ApplicationGatewaysClientListAllResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, agw := range page.Value {
				if agw == nil || agw.ID == nil {
					continue
				}
				name, loc := sv(agw.Name), sv(agw.Location)
				nativeID := sv(agw.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeNetworkApplicationGateway, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(agw.Tags), AttributesJSON: mustJSON(agw),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeNetworkApplicationGateway, nativeID))
				}
			}
			return batch, pairs
		})
}
