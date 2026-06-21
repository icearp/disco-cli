package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/relay/armrelay"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.relay",
		fn:   scanRelay,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.relay", DiscoType: TypeRelayNamespace, Leaf: true},
		},
	})
}

// scanRelay discovers Azure Relay namespaces.
func scanRelay(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armrelay.NewNamespacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armrelay:NewNamespacesClient: %w", err)
	}
	return azSimpleScan(ctx, "armrelay:Namespaces.List", TypeRelayNamespace, sub, st, scanID,
		client.NewListPager(nil),
		func(p armrelay.NamespacesClientListResponse) []*armrelay.Namespace { return p.Value },
		func(n *armrelay.Namespace) azTrackedBase {
			return azTrackedBase{id: sv(n.ID), name: sv(n.Name), location: sv(n.Location), tags: n.Tags, full: n}
		})
}
