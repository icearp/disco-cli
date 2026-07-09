package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devopsinfrastructure/armdevopsinfrastructure"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDevOpsInfrastructurePool, Service: "microsoft.devopsinfrastructure", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.devopsinfrastructure",
		fn:   scanDevOpsInfrastructure,
	})
}

// scanDevOpsInfrastructure discovers Microsoft Managed DevOps Pools.
func scanDevOpsInfrastructure(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdevopsinfrastructure.NewPoolsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdevopsinfrastructure:NewPoolsClient: %w", err)
	}
	return azSimpleScan(ctx, "armdevopsinfrastructure:Pools.ListBySubscription", TypeDevOpsInfrastructurePool, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdevopsinfrastructure.PoolsClientListBySubscriptionResponse) []*armdevopsinfrastructure.Pool {
			return p.Value
		},
		func(p *armdevopsinfrastructure.Pool) azTrackedBase {
			return azTrackedBase{id: sv(p.ID), name: sv(p.Name), location: sv(p.Location), tags: p.Tags, full: p}
		})
}
