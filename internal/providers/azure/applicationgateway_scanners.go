package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

func init() {
	registerService(serviceEntry{
		name: "azure:applicationgateway",
		fn:   scanApplicationGateway,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.network", DiscoType: TypeNetworkApplicationGateway},
		},
	})
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
	return azSimpleScan(ctx, "armnetwork:ApplicationGateways.ListAll", TypeNetworkApplicationGateway, sub, st, scanID,
		client.NewListAllPager(nil),
		func(p armnetwork.ApplicationGatewaysClientListAllResponse) []*armnetwork.ApplicationGateway {
			return p.Value
		},
		func(agw *armnetwork.ApplicationGateway) azTrackedBase {
			return azTrackedBase{id: sv(agw.ID), name: sv(agw.Name), location: sv(agw.Location), tags: agw.Tags, full: agw}
		})
}
