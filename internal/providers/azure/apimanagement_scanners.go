package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
)

func init() {
	registerService(serviceEntry{
		name: "azure:apimanagement",
		fn:   scanAPIManagement,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.apimanagement", DiscoType: TypeAPIManagementService},
		},
	})
}

// scanAPIManagement discovers Azure API Management service instances. APIs,
// products, policies, subscriptions, named values, certificates, backends,
// gateways, and identity providers deferred — sub-resources whose graph
// value lives in the service's identity + KeyVault edges.
func scanAPIManagement(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armapimanagement.NewServiceClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armapimanagement:NewServiceClient: %w", err)
	}
	return azSimpleScan(ctx, "armapimanagement:Service.List", TypeAPIManagementService, sub, st, scanID,
		client.NewListPager(nil),
		func(p armapimanagement.ServiceClientListResponse) []*armapimanagement.ServiceResource { return p.Value },
		func(s *armapimanagement.ServiceResource) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
}
