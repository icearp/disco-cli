package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/fluidrelay/armfluidrelay"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.fluidrelay",
		fn:   scanFluidRelay,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.fluidrelay", DiscoType: TypeFluidRelayServer, Leaf: true},
		},
	})
}

// scanFluidRelay discovers fluidrelay resources.
func scanFluidRelay(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armfluidrelay.NewServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armfluidrelay:NewServersClient: %w", err)
	}
	return azSimpleScan(ctx, "armfluidrelay:Servers.ListBySubscription", TypeFluidRelayServer, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armfluidrelay.ServersClientListBySubscriptionResponse) []*armfluidrelay.Server {
			return p.Value
		},
		func(r *armfluidrelay.Server) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
