package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sphere/armsphere"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAzureSphereCatalog, Service: "microsoft.azuresphere", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.azuresphere",
		fn:   scanAzureSphere,
	})
}

// scanAzureSphere discovers azuresphere resources.
func scanAzureSphere(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
