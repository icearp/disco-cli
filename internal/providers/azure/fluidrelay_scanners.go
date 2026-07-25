package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/fluidrelay/armfluidrelay"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeFluidRelayServer, Service: "microsoft.fluidrelay", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.fluidrelay",
		fn:   scanFluidRelay,
	})
}

// scanFluidRelay discovers fluidrelay resources.
func scanFluidRelay(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
