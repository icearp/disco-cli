package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/planetarycomputer/armplanetarycomputer"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOrbitalGeoCatalog, Service: "microsoft.orbital"})
	// Microsoft.Orbital/geoCatalogs (Planetary Computer Pro) is the only live
	// type left in Microsoft.Orbital — Spacecrafts (ground-station) retired
	// Dec 2024 — so this scanner owns the azure:microsoft.orbital registration.
	registerService(serviceEntry{
		name: "azure:microsoft.orbital",
		fn:   scanPlanetaryComputer,
	})
}

// scanPlanetaryComputer discovers Microsoft Planetary Computer GeoCatalogs
// (surfaced under the Microsoft.Orbital/geoCatalogs ARM namespace).
func scanPlanetaryComputer(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
