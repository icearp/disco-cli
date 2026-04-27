package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/synapse/armsynapse"
)

func init() { registerService(serviceEntry{name: "azure:synapse", fn: scanSynapse}) }

// scanSynapse discovers Azure Synapse Analytics workspaces. SQL pools, Spark
// pools, integration runtimes, private endpoint connections, and managed
// private endpoints deferred — sub-resources whose graph value lives in the
// workspace-level edges (default ADLS, managed VNet, CMEK).
func scanSynapse(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armsynapse.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsynapse:NewWorkspacesClient: %w", err)
	}
	return azPageScan(ctx, "armsynapse:Workspaces.List", sub, st,
		client.NewListPager(nil),
		func(page armsynapse.WorkspacesClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, w := range page.Value {
				if w == nil || w.ID == nil {
					continue
				}
				name, loc := sv(w.Name), sv(w.Location)
				nativeID := sv(w.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeSynapseWorkspace, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(w.Tags), AttributesJSON: mustJSON(w),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeSynapseWorkspace, nativeID))
				}
			}
			return batch, pairs
		})
}
