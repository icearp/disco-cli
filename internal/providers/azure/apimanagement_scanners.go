package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAPIManagementService, Service: "microsoft.apimanagement"})
	registerService(serviceEntry{
		name: "azure:microsoft.apimanagement",
		fn:   scanAPIManagement,
	})
}

// scanAPIManagement discovers Azure API Management service instances. APIs,
// products, policies, subscriptions, named values, certificates, backends,
// gateways, and identity providers deferred — sub-resources whose graph
// value lives in the service's identity + KeyVault edges.
func scanAPIManagement(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
