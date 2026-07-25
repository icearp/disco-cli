package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/saas/armsaas"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSaaSApplication, Service: "microsoft.saas"})
	registerType(restype.Descriptor{Type: TypeSaaSResource, Service: "microsoft.saas"})
	registerService(serviceEntry{
		name: "azure:microsoft.saas",
		fn:   scanSaaS,
	})
}

// scanSaaS discovers Microsoft.SaaS applications (RG-scoped, fanned out per RG)
// and subscription-level SaaS resources. SaaS Resource items carry tags but no
// location.
func scanSaaS(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			appClient, err := armsaas.NewApplicationsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armsaas:NewApplicationsClient: %w", err)
			}
			return azRGFanoutScan(ctx, "armsaas:Applications.List", TypeSaaSApplication, sub, cred, st, scanID,
				func(rg string) azPager[armsaas.ApplicationsClientListResponse] {
					return appClient.NewListPager(rg, nil)
				},
				func(p armsaas.ApplicationsClientListResponse) []*armsaas.App { return p.Value },
				func(r *armsaas.App) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			subClient, err := armsaas.NewSubscriptionLevelClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armsaas:NewSubscriptionLevelClient: %w", err)
			}
			return azSimpleScan(ctx, "armsaas:SubscriptionLevel.ListByAzureSubscription", TypeSaaSResource, sub, st, scanID,
				subClient.NewListByAzureSubscriptionPager(nil),
				func(p armsaas.SubscriptionLevelClientListByAzureSubscriptionResponse) []*armsaas.Resource {
					return p.Value
				},
				func(r *armsaas.Resource) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), tags: r.Tags, full: r}
				})
		},
	)
}
