package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/healthcareapis/armhealthcareapis"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.healthcareapis",
		fn:   scanHealthcareAPIs,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.healthcareapis", DiscoType: TypeHealthcareAPIsService, Leaf: true},
			{Service: "microsoft.healthcareapis", DiscoType: TypeHealthcareAPIsWorkspace, Leaf: true},
		},
	})
}

// scanHealthcareAPIs discovers Azure Health Data Services: account-level
// service instances (DICOM/FHIR/IoT connectors of the classic shape) and the
// newer workspaces.
func scanHealthcareAPIs(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			svcClient, err := armhealthcareapis.NewServicesClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armhealthcareapis:NewServicesClient: %w", err)
			}
			return azSimpleScan(ctx, "armhealthcareapis:Services.List", TypeHealthcareAPIsService, sub, st, scanID,
				svcClient.NewListPager(nil),
				func(p armhealthcareapis.ServicesClientListResponse) []*armhealthcareapis.ServicesDescription {
					return p.Value
				},
				func(s *armhealthcareapis.ServicesDescription) azTrackedBase {
					return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
				})
		},
		func() (int, int, error) {
			wsClient, err := armhealthcareapis.NewWorkspacesClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armhealthcareapis:NewWorkspacesClient: %w", err)
			}
			return azSimpleScan(ctx, "armhealthcareapis:Workspaces.ListBySubscription", TypeHealthcareAPIsWorkspace, sub, st, scanID,
				wsClient.NewListBySubscriptionPager(nil),
				func(p armhealthcareapis.WorkspacesClientListBySubscriptionResponse) []*armhealthcareapis.Workspace {
					return p.Value
				},
				func(w *armhealthcareapis.Workspace) azTrackedBase {
					return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
				})
		},
	)
}
