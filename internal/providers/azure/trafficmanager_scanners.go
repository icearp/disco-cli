package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/trafficmanager/armtrafficmanager"
)

func init() {
	registerService(serviceEntry{
		name: "azure:trafficmanager",
		fn:   scanTrafficManager,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.network", DiscoType: TypeNetworkTrafficManagerProfile},
		},
	})
}

// scanTrafficManager discovers Azure Traffic Manager profiles. Endpoints are
// embedded in the profile's properties.endpoints[] array (full body returned
// by ListBySubscription), so a separate endpoint scan is not required.
// Heatmap, geographic-hierarchy, and user-metric-keys APIs deferred — config
// surfaces, not edge sources.
func scanTrafficManager(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armtrafficmanager.NewProfilesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armtrafficmanager:NewProfilesClient: %w", err)
	}
	return azSimpleScan(ctx, "armtrafficmanager:Profiles.ListBySubscription", TypeNetworkTrafficManagerProfile, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armtrafficmanager.ProfilesClientListBySubscriptionResponse) []*armtrafficmanager.Profile {
			return p.Value
		},
		func(p *armtrafficmanager.Profile) azTrackedBase {
			return azTrackedBase{id: sv(p.ID), name: sv(p.Name), location: sv(p.Location), tags: p.Tags, full: p}
		})
}
