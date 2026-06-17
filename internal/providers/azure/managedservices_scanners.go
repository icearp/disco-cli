package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managedservices/armmanagedservices"
)

func init() {
	registerService(serviceEntry{
		name: "azure:managedservices",
		fn:   scanManagedServices,
		emits: []coverage.TypeDecl{
			// Azure Lighthouse delegations — cross-tenant access grants. The
			// definition references the managing tenant by GUID, not an in-scope
			// ARM resource, so it ships scanner-only.
			{Service: "microsoft.managedservices", DiscoType: TypeManagedServicesRegistrationDefinition, Leaf: true},
		},
	})
}

// scanManagedServices discovers Azure Lighthouse registration definitions
// (delegated resource-management grants) at subscription scope.
func scanManagedServices(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmanagedservices.NewRegistrationDefinitionsClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagedservices:NewRegistrationDefinitionsClient: %w", err)
	}
	return azSimpleScan(ctx, "armmanagedservices:RegistrationDefinitions.List", TypeManagedServicesRegistrationDefinition, sub, st, scanID,
		client.NewListPager("subscriptions/"+sub.ID, nil),
		func(p armmanagedservices.RegistrationDefinitionsClientListResponse) []*armmanagedservices.RegistrationDefinition {
			return p.Value
		},
		func(d *armmanagedservices.RegistrationDefinition) azTrackedBase {
			return azTrackedBase{id: sv(d.ID), name: sv(d.Name), full: d}
		})
}
