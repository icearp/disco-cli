package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/integrationspaces/armintegrationspaces"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.integrationspaces",
		fn:   scanIntegrationSpaces,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.integrationspaces", DiscoType: TypeIntegrationSpace, Leaf: true},
		},
	})
}

// scanIntegrationSpaces discovers integrationspaces resources.
func scanIntegrationSpaces(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armintegrationspaces.NewSpacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armintegrationspaces:NewSpacesClient: %w", err)
	}
	return azSimpleScan(ctx, "armintegrationspaces:Spaces.ListBySubscription", TypeIntegrationSpace, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armintegrationspaces.SpacesClientListBySubscriptionResponse) []*armintegrationspaces.Space {
			return p.Value
		},
		func(r *armintegrationspaces.Space) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
