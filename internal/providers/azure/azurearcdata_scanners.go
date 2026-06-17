package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/azurearcdata/armazurearcdata"
)

func init() {
	registerService(serviceEntry{
		name: "azure:azurearcdata",
		fn:   scanAzureArcData,
		emits: []coverage.TypeDecl{
			// Custom-location edge wired centrally (resolveExtendedLocationConsumers);
			// scanner-only here.
			{Service: "microsoft.azurearcdata", DiscoType: TypeAzureArcDataController, Leaf: true},
		},
	})
}

// scanAzureArcData discovers Azure Arc-enabled data controllers.
func scanAzureArcData(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armazurearcdata.NewDataControllersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armazurearcdata:NewDataControllersClient: %w", err)
	}
	return azSimpleScan(ctx, "armazurearcdata:DataControllers.ListInSubscription", TypeAzureArcDataController, sub, st, scanID,
		client.NewListInSubscriptionPager(nil),
		func(p armazurearcdata.DataControllersClientListInSubscriptionResponse) []*armazurearcdata.DataControllerResource {
			return p.Value
		},
		func(d *armazurearcdata.DataControllerResource) azTrackedBase {
			return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
		})
}
