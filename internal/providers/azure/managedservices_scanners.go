package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managedservices/armmanagedservices"
)

func init() {
	registerType(restype.Descriptor{Type: TypeManagedServicesRegistrationDefinition, Service: "microsoft.managedservices", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedServicesMarketplaceRegDef, Service: "microsoft.managedservices", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedServicesRegistrationAssign, Service: "microsoft.managedservices", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.managedservices",
		fn:   scanManagedServices,
	})
}

// scanManagedServices discovers Azure Lighthouse registration definitions,
// marketplace registration definitions, and registration assignments at
// subscription scope.
func scanManagedServices(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	scope := "subscriptions/" + sub.ID
	regDefs, err := armmanagedservices.NewRegistrationDefinitionsClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagedservices:NewRegistrationDefinitionsClient: %w", err)
	}
	mktDefs, err := armmanagedservices.NewMarketplaceRegistrationDefinitionsClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagedservices:NewMarketplaceRegistrationDefinitionsClient: %w", err)
	}
	assigns, err := armmanagedservices.NewRegistrationAssignmentsClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagedservices:NewRegistrationAssignmentsClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagedservices:RegistrationDefinitions.List", TypeManagedServicesRegistrationDefinition, sub, st, scanID,
				regDefs.NewListPager(scope, nil),
				func(p armmanagedservices.RegistrationDefinitionsClientListResponse) []*armmanagedservices.RegistrationDefinition {
					return p.Value
				},
				func(d *armmanagedservices.RegistrationDefinition) azTrackedBase {
					return azTrackedBase{id: sv(d.ID), name: sv(d.Name), full: d}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagedservices:MarketplaceRegistrationDefinitions.List", TypeManagedServicesMarketplaceRegDef, sub, st, scanID,
				mktDefs.NewListPager(scope, nil),
				func(p armmanagedservices.MarketplaceRegistrationDefinitionsClientListResponse) []*armmanagedservices.MarketplaceRegistrationDefinition {
					return p.Value
				},
				func(r *armmanagedservices.MarketplaceRegistrationDefinition) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagedservices:RegistrationAssignments.List", TypeManagedServicesRegistrationAssign, sub, st, scanID,
				assigns.NewListPager(scope, nil),
				func(p armmanagedservices.RegistrationAssignmentsClientListResponse) []*armmanagedservices.RegistrationAssignment {
					return p.Value
				},
				func(r *armmanagedservices.RegistrationAssignment) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), full: r}
				})
		},
	)
}
