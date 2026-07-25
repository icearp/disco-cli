package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resiliencemanagement/armresiliencemanagement"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeResilienceUsagePlan, Service: "microsoft.azureresiliencemanagement"})
	registerService(serviceEntry{
		name: "azure:microsoft.azureresiliencemanagement",
		fn:   scanResilienceManagement,
	})
}

// scanResilienceManagement discovers Azure Resilience usage plans. Other
// resilience resources (alerts, experiments, fault simulations) are
// parent-scoped (per usage-plan) and deferred.
func scanResilienceManagement(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armresiliencemanagement.NewUsagePlansClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armresiliencemanagement:NewUsagePlansClient: %w", err)
	}
	return azSimpleScan(ctx, "armresiliencemanagement:UsagePlans.ListBySubscription", TypeResilienceUsagePlan, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armresiliencemanagement.UsagePlansClientListBySubscriptionResponse) []*armresiliencemanagement.UsagePlan {
			return p.Value
		},
		func(r *armresiliencemanagement.UsagePlan) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
