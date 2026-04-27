package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datafactory/armdatafactory"
)

func init() { registerService(serviceEntry{name: "azure:datafactory", fn: scanDataFactory}) }

// scanDataFactory discovers Azure Data Factory factories. Linked services,
// pipelines, datasets, dataflows, triggers, integration runtimes, and managed
// virtual network sub-resources deferred — pipeline graphs would explode in
// volume and most edges (linked services → backing resources) live in
// per-linked-service typed payloads that warrant their own resolver pass.
func scanDataFactory(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdatafactory.NewFactoriesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdatafactory:NewFactoriesClient: %w", err)
	}
	return azPageScan(ctx, "armdatafactory:Factories.List", sub, st,
		client.NewListPager(nil),
		func(page armdatafactory.FactoriesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, f := range page.Value {
				if f == nil || f.ID == nil {
					continue
				}
				name, loc := sv(f.Name), sv(f.Location)
				nativeID := sv(f.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeDataFactoryFactory, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(f.Tags), AttributesJSON: mustJSON(f),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeDataFactoryFactory, nativeID))
				}
			}
			return batch, pairs
		})
}
