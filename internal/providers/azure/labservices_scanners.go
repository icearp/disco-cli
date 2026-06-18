package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/labservices/armlabservices"
)

func init() {
	registerService(serviceEntry{
		name: "azure:labservices",
		fn:   scanLabServices,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.labservices", DiscoType: TypeLabServicesLab, Leaf: true},
			{Service: "microsoft.labservices", DiscoType: TypeLabServicesLabPlan, Leaf: true},
		},
	})
}

// scanLabServices discovers Azure Lab Services labs and lab plans.
func scanLabServices(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	labs, err := armlabservices.NewLabsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armlabservices:NewLabsClient: %w", err)
	}
	plans, err := armlabservices.NewLabPlansClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armlabservices:NewLabPlansClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armlabservices:Labs.ListBySubscription", TypeLabServicesLab, sub, st, scanID,
				labs.NewListBySubscriptionPager(nil),
				func(p armlabservices.LabsClientListBySubscriptionResponse) []*armlabservices.Lab { return p.Value },
				func(r *armlabservices.Lab) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armlabservices:LabPlans.ListBySubscription", TypeLabServicesLabPlan, sub, st, scanID,
				plans.NewListBySubscriptionPager(nil),
				func(p armlabservices.LabPlansClientListBySubscriptionResponse) []*armlabservices.LabPlan {
					return p.Value
				},
				func(r *armlabservices.LabPlan) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
