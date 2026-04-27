package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/trafficmanager/armtrafficmanager"
)

func init() {
	registerService(serviceEntry{name: "azure:trafficmanager", fn: scanTrafficManager})
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
	return azPageScan(ctx, "armtrafficmanager:Profiles.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armtrafficmanager.ProfilesClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
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
					Type: TypeNetworkTrafficManagerProfile, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(p.Tags), AttributesJSON: mustJSON(p),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeNetworkTrafficManagerProfile, nativeID))
				}
			}
			return batch, pairs
		})
}
