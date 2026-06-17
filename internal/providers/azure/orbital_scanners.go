package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/orbital/armorbital"
)

func init() {
	registerService(serviceEntry{
		name: "azure:orbital",
		fn:   scanOrbital,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.orbital", DiscoType: TypeOrbitalSpacecraft, Leaf: true},
		},
	})
}

// scanOrbital discovers Azure Orbital spacecrafts.
func scanOrbital(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armorbital.NewSpacecraftsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armorbital:NewSpacecraftsClient: %w", err)
	}
	return azSimpleScan(ctx, "armorbital:Spacecrafts.ListBySubscription", TypeOrbitalSpacecraft, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armorbital.SpacecraftsClientListBySubscriptionResponse) []*armorbital.Spacecraft {
			return p.Value
		},
		func(s *armorbital.Spacecraft) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
}
