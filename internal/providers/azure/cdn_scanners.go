package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cdn/armcdn"
)

func init() {
	registerService(serviceEntry{
		name: "azure:cdn",
		fn:   scanCDN,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.cdn", DiscoType: TypeCDNProfile},
		},
	})
}

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
	return azSimpleScan(ctx, "armcdn:Profiles.List", TypeCDNProfile, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcdn.ProfilesClientListResponse) []*armcdn.Profile { return p.Value },
		func(p *armcdn.Profile) azTrackedBase {
			return azTrackedBase{id: sv(p.ID), name: sv(p.Name), location: sv(p.Location), tags: p.Tags, full: p}
		})
}
