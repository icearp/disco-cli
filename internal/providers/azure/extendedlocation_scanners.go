package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/extendedlocation/armextendedlocation"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.extendedlocation",
		fn:   scanExtendedLocation,
		emits: []coverage.TypeDecl{
			// resolveCustomLocationRelationships wires the host (connected
			// cluster / appliance / AKS) edge below.
			{Service: "microsoft.extendedlocation", DiscoType: TypeCustomLocation},
		},
	})
}

// scanExtendedLocation discovers Azure Arc custom locations.
func scanExtendedLocation(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armextendedlocation.NewCustomLocationsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armextendedlocation:NewCustomLocationsClient: %w", err)
	}
	return azSimpleScan(ctx, "armextendedlocation:CustomLocations.ListBySubscription", TypeCustomLocation, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armextendedlocation.CustomLocationsClientListBySubscriptionResponse) []*armextendedlocation.CustomLocation {
			return p.Value
		},
		func(c *armextendedlocation.CustomLocation) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
