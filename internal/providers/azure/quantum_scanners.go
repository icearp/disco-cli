package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/quantum/armquantum"
)

func init() {
	registerService(serviceEntry{
		name: "azure:quantum",
		fn:   scanQuantum,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; the workspace references
			// a storage account by name (not ARM ID), so it ships scanner-only.
			{Service: "microsoft.quantum", DiscoType: TypeQuantumWorkspace, Leaf: true},
		},
	})
}

// scanQuantum discovers Azure Quantum workspaces.
func scanQuantum(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armquantum.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armquantum:NewWorkspacesClient: %w", err)
	}
	return azSimpleScan(ctx, "armquantum:Workspaces.ListBySubscription", TypeQuantumWorkspace, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armquantum.WorkspacesClientListBySubscriptionResponse) []*armquantum.Workspace { return p.Value },
		func(w *armquantum.Workspace) azTrackedBase {
			return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
		})
}
