package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/trafficmanager/armtrafficmanager"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNetworkTrafficManagerProfile, Service: "microsoft.network"})
}

// scanTrafficManager discovers Azure Traffic Manager profiles. Endpoints are
// embedded in properties.endpoints[] (full body from ListBySubscription), so
// no separate endpoint scan is needed. Heatmap, geographic-hierarchy, and
// user-metric-keys APIs deferred — config surfaces, not edge sources.
func scanTrafficManager(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
