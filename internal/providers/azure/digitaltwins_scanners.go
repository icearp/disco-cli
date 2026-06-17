package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/digitaltwins/armdigitaltwins"
)

func init() {
	registerService(serviceEntry{
		name: "azure:digitaltwins",
		fn:   scanDigitalTwins,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.digitaltwins", DiscoType: TypeDigitalTwinsInstance, Leaf: true},
		},
	})
}

// scanDigitalTwins discovers Azure Digital Twins instances.
func scanDigitalTwins(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdigitaltwins.NewClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdigitaltwins:NewClient: %w", err)
	}
	return azSimpleScan(ctx, "armdigitaltwins:List", TypeDigitalTwinsInstance, sub, st, scanID,
		client.NewListPager(nil),
		func(p armdigitaltwins.ClientListResponse) []*armdigitaltwins.Description { return p.Value },
		func(d *armdigitaltwins.Description) azTrackedBase {
			return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
		})
}
