package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/planetarycomputer/armplanetarycomputer"
)

func init() {
	// Microsoft.Orbital/geoCatalogs (Planetary Computer Pro) is the only live
	// type left in the Microsoft.Orbital namespace — the ground-station
	// Spacecrafts service was retired Dec 2024 — so this scanner owns the
	// azure:microsoft.orbital service registration.
	registerService(serviceEntry{
		name: "azure:microsoft.orbital",
		fn:   scanPlanetaryComputer,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.orbital", DiscoType: TypeOrbitalGeoCatalog},
		},
	})
}

// scanPlanetaryComputer discovers Microsoft Planetary Computer GeoCatalogs
// (surfaced under the Microsoft.Orbital/geoCatalogs ARM namespace).
func scanPlanetaryComputer(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armplanetarycomputer.NewGeoCatalogsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armplanetarycomputer:NewGeoCatalogsClient: %w", err)
	}
	return azSimpleScan(ctx, "armplanetarycomputer:GeoCatalogs.ListBySubscription", TypeOrbitalGeoCatalog, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armplanetarycomputer.GeoCatalogsClientListBySubscriptionResponse) []*armplanetarycomputer.GeoCatalog {
			return p.Value
		},
		func(r *armplanetarycomputer.GeoCatalog) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
