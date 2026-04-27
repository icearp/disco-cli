package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement"
)

func init() { registerService(serviceEntry{name: "azure:apimanagement", fn: scanAPIManagement}) }

// scanAPIManagement discovers Azure API Management service instances. APIs,
// products, policies, subscriptions, named values, certificates, backends,
// gateways, and identity providers deferred — sub-resources whose graph
// value lives in the service's identity + KeyVault edges.
func scanAPIManagement(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armapimanagement.NewServiceClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armapimanagement:NewServiceClient: %w", err)
	}
	return azPageScan(ctx, "armapimanagement:Service.List", sub, st,
		client.NewListPager(nil),
		func(page armapimanagement.ServiceClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, s := range page.Value {
				if s == nil || s.ID == nil {
					continue
				}
				name, loc := sv(s.Name), sv(s.Location)
				nativeID := sv(s.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeAPIManagementService, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(s.Tags), AttributesJSON: mustJSON(s),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeAPIManagementService, nativeID))
				}
			}
			return batch, pairs
		})
}
