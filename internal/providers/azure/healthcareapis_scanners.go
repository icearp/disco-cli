package azure

import (
	"context"
	"errors"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
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
func scanHealthcareAPIs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	svcClient, err := armhealthcareapis.NewServicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhealthcareapis:NewServicesClient: %w", err)
	}
	wsClient, err := armhealthcareapis.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhealthcareapis:NewWorkspacesClient: %w", err)
	}

	t1, i1, e1 := azSimpleScan(ctx, "armhealthcareapis:Services.List", TypeHealthcareAPIsService, sub, st, scanID,
		svcClient.NewListPager(nil),
		func(p armhealthcareapis.ServicesClientListResponse) []*armhealthcareapis.ServicesDescription {
			return p.Value
		},
		func(s *armhealthcareapis.ServicesDescription) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
	t2, i2, e2 := azSimpleScan(ctx, "armhealthcareapis:Workspaces.ListBySubscription", TypeHealthcareAPIsWorkspace, sub, st, scanID,
		wsClient.NewListBySubscriptionPager(nil),
		func(p armhealthcareapis.WorkspacesClientListBySubscriptionResponse) []*armhealthcareapis.Workspace {
			return p.Value
		},
		func(w *armhealthcareapis.Workspace) azTrackedBase {
			return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
		})
	return t1 + t2, i1 + i2, errors.Join(e1, e2)
}
