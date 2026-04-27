package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cdn/armcdn"
)

func init() { registerService(serviceEntry{name: "azure:cdn", fn: scanCDN}) }

// scanCDN discovers Azure Front Door + classic CDN profiles. Both surface
// under `Microsoft.Cdn/profiles`; the SKU.Name field differentiates Front
// Door Standard / Premium (`Standard_AzureFrontDoor` / `Premium_AzureFrontDoor`)
// from classic CDN profiles (`Standard_Microsoft`, `Standard_Verizon`, etc.).
// Endpoints (CDN), AFD endpoints, origin groups, origins, routes, rule sets
// deferred — sub-resources whose graph value lives in the profile-level
// origin-target edges; punch list for follow-up.
func scanCDN(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcdn.NewProfilesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcdn:NewProfilesClient: %w", err)
	}
	return azPageScan(ctx, "armcdn:Profiles.List", sub, st,
		client.NewListPager(nil),
		func(page armcdn.ProfilesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, p := range page.Value {
				if p == nil || p.ID == nil {
					continue
				}
				name, loc := sv(p.Name), sv(p.Location)
				nativeID := sv(p.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeCDNProfile, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(p.Tags), AttributesJSON: mustJSON(p),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeCDNProfile, nativeID))
				}
			}
			return batch, pairs
		})
}
