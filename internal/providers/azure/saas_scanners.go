package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/saas/armsaas"
)

func init() {
	registerService(serviceEntry{
		name: "azure:saas",
		fn:   scanSaaS,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.saas", DiscoType: TypeSaaSApplication},
			{Service: "microsoft.saas", DiscoType: TypeSaaSResource},
		},
	})
}

// scanSaaS discovers Microsoft.SaaS applications (RG-scoped, fanned out per RG)
// and subscription-level SaaS resources. SaaS Resource items carry tags but no
// location.
func scanSaaS(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	appClient, err := armsaas.NewApplicationsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsaas:NewApplicationsClient: %w", err)
	}
	at, ai, err := azRGFanoutScan(ctx, "armsaas:Applications.List", TypeSaaSApplication, sub, cred, st, scanID,
		func(rg string) azPager[armsaas.ApplicationsClientListResponse] {
			return appClient.NewListPager(rg, nil)
		},
		func(p armsaas.ApplicationsClientListResponse) []*armsaas.App { return p.Value },
		func(r *armsaas.App) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
	total += at
	inserted += ai
	if err != nil {
		return total, inserted, err
	}

	subClient, err := armsaas.NewSubscriptionLevelClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armsaas:NewSubscriptionLevelClient: %w", err)
	}
	rt, ri, err := azSimpleScan(ctx, "armsaas:SubscriptionLevel.ListByAzureSubscription", TypeSaaSResource, sub, st, scanID,
		subClient.NewListByAzureSubscriptionPager(nil),
		func(p armsaas.SubscriptionLevelClientListByAzureSubscriptionResponse) []*armsaas.Resource {
			return p.Value
		},
		func(r *armsaas.Resource) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), tags: r.Tags, full: r}
		})
	total += rt
	inserted += ri
	return total, inserted, err
}
