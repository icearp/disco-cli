package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/springappdiscovery/armspringappdiscovery"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.offazurespringboot",
		fn:   scanSpringAppDiscovery,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.offazurespringboot", DiscoType: TypeSpringbootSite, Leaf: true},
		},
	})
}

// scanSpringAppDiscovery discovers springappdiscovery resources.
func scanSpringAppDiscovery(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armspringappdiscovery.NewSpringbootsitesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armspringappdiscovery:NewSpringbootsitesClient: %w", err)
	}
	return azSimpleScan(ctx, "armspringappdiscovery:Springbootsites.ListBySubscription", TypeSpringbootSite, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armspringappdiscovery.SpringbootsitesClientListBySubscriptionResponse) []*armspringappdiscovery.SpringbootsitesModel {
			return p.Value
		},
		func(r *armspringappdiscovery.SpringbootsitesModel) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
