package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devopsinfrastructure/armdevopsinfrastructure"
)

func init() {
	registerService(serviceEntry{
		name: "azure:devopsinfrastructure",
		fn:   scanDevOpsInfrastructure,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; the managed DevOps pool
			// ships scanner-only.
			{Service: "microsoft.devopsinfrastructure", DiscoType: TypeDevOpsInfrastructurePool, Leaf: true},
		},
	})
}

// scanDevOpsInfrastructure discovers Microsoft Managed DevOps Pools.
func scanDevOpsInfrastructure(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
