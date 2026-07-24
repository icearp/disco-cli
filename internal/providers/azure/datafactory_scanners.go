package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datafactory/armdatafactory"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDataFactoryFactory, Service: "microsoft.datafactory"})
	registerService(serviceEntry{
		name: "azure:microsoft.datafactory",
		fn:   scanDataFactory,
	})
}

// scanDataFactory discovers Azure Data Factory factories. Linked services,
// pipelines, datasets, dataflows, triggers, integration runtimes, and managed
// VNet sub-resources deferred — pipeline graphs would explode in volume, and
// most edges (linked services → backing resources) live in per-linked-service
// typed payloads warranting their own resolver pass.
func scanDataFactory(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdatafactory.NewFactoriesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdatafactory:NewFactoriesClient: %w", err)
	}
	return azSimpleScan(ctx, "armdatafactory:Factories.List", TypeDataFactoryFactory, sub, st, scanID,
		client.NewListPager(nil),
		func(p armdatafactory.FactoriesClientListResponse) []*armdatafactory.Factory { return p.Value },
		func(f *armdatafactory.Factory) azTrackedBase {
			return azTrackedBase{id: sv(f.ID), name: sv(f.Name), location: sv(f.Location), tags: f.Tags, full: f}
		})
}
