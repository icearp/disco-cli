package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sphere/armsphere"
)

func init() {
	registerService(serviceEntry{
		name: "azure:azuresphere",
		fn:   scanAzureSphere,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.azuresphere", DiscoType: TypeAzureSphereCatalog, Leaf: true},
		},
	})
}

// scanAzureSphere discovers azuresphere resources.
func scanAzureSphere(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armsphere.NewCatalogsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsphere:NewCatalogsClient: %w", err)
	}
	return azSimpleScan(ctx, "armsphere:Catalogs.ListBySubscription", TypeAzureSphereCatalog, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armsphere.CatalogsClientListBySubscriptionResponse) []*armsphere.Catalog { return p.Value },
		func(r *armsphere.Catalog) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
