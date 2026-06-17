package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/automanage/armautomanage"
)

func init() {
	registerService(serviceEntry{
		name: "azure:automanage",
		fn:   scanAutomanage,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.automanage", DiscoType: TypeAutomanageConfigProfile, Leaf: true},
		},
	})
}

// scanAutomanage discovers automanage resources.
func scanAutomanage(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armautomanage.NewConfigurationProfilesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armautomanage:NewConfigurationProfilesClient: %w", err)
	}
	return azSimpleScan(ctx, "armautomanage:ConfigurationProfiles.ListBySubscription", TypeAutomanageConfigProfile, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armautomanage.ConfigurationProfilesClientListBySubscriptionResponse) []*armautomanage.ConfigurationProfile {
			return p.Value
		},
		func(r *armautomanage.ConfigurationProfile) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
