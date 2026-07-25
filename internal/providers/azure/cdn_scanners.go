package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cdn/armcdn"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCDNProfile, Service: "microsoft.cdn"})
	registerType(restype.Descriptor{Type: TypeCDNWAFPolicy, Service: "microsoft.cdn"})
	registerService(serviceEntry{
		name: "azure:microsoft.cdn",
		fn:   scanCDN,
	})
}

// scanCDN discovers Azure Front Door + classic CDN profiles plus the
// Front Door / CDN WAF policies. Profiles surface under
// `Microsoft.Cdn/profiles`; the SKU.Name field differentiates Front Door
// Standard / Premium (`Standard_AzureFrontDoor` / `Premium_AzureFrontDoor`)
// from classic CDN profiles (`Standard_Microsoft`, `Standard_Verizon`, etc.).
// WAF policies have no subscription-wide list op — only per-RG — so they fan
// out via azRGFanoutScan. Endpoints (CDN), AFD endpoints, origin groups,
// origins, routes, rule sets deferred — sub-resources whose graph value lives
// in the profile-level origin-target edges; punch list for follow-up.
func scanCDN(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			profClient, err := armcdn.NewProfilesClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armcdn:NewProfilesClient: %w", err)
			}
			return azSimpleScan(ctx, "armcdn:Profiles.List", TypeCDNProfile, sub, st, scanID,
				profClient.NewListPager(nil),
				func(p armcdn.ProfilesClientListResponse) []*armcdn.Profile { return p.Value },
				func(p *armcdn.Profile) azTrackedBase {
					return azTrackedBase{id: sv(p.ID), name: sv(p.Name), location: sv(p.Location), tags: p.Tags, full: p}
				})
		},
		func() (int, int, error) {
			polClient, err := armcdn.NewPoliciesClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armcdn:NewPoliciesClient: %w", err)
			}
			return azRGFanoutScan(ctx, "armcdn:Policies.List", TypeCDNWAFPolicy, sub, cred, st, scanID,
				func(rg string) azPager[armcdn.PoliciesClientListResponse] {
					return polClient.NewListPager(rg, nil)
				},
				func(p armcdn.PoliciesClientListResponse) []*armcdn.WebApplicationFirewallPolicy { return p.Value },
				func(r *armcdn.WebApplicationFirewallPolicy) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
