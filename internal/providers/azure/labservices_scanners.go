package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/labservices/armlabservices"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeLabServicesLab, Service: "microsoft.labservices", Leaf: true, Redact: []redact.Rule{{Path: "properties.virtualMachineProfile.adminUser.password", Mode: redact.RedactScalar}, {Path: "properties.virtualMachineProfile.nonAdminUser.password", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeLabServicesLabPlan, Service: "microsoft.labservices", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.labservices",
		fn:   scanLabServices,
	})
}

// scanLabServices discovers Azure Lab Services labs and lab plans.
func scanLabServices(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
