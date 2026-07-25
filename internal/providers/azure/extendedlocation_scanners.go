package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/extendedlocation/armextendedlocation"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCustomLocation, Service: "microsoft.extendedlocation", Redact: []redact.Rule{{Path: "properties.authentication.value", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "azure:microsoft.extendedlocation",
		fn:   scanExtendedLocation,
	})
}

// scanExtendedLocation discovers Azure Arc custom locations.
func scanExtendedLocation(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
