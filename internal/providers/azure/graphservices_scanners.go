package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/graphservices/armgraphservices"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.graphservices",
		fn:   scanGraphServices,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.graphservices", DiscoType: TypeGraphServicesAccount, Leaf: true},
		},
	})
}

// scanGraphServices discovers graphservices resources.
func scanGraphServices(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armgraphservices.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armgraphservices:NewAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armgraphservices:Accounts.ListBySubscription", TypeGraphServicesAccount, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armgraphservices.AccountsClientListBySubscriptionResponse) []*armgraphservices.AccountResource {
			return p.Value
		},
		func(r *armgraphservices.AccountResource) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
