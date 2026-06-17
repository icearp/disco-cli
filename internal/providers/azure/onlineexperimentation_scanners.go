package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/onlineexperimentation/armonlineexperimentation"
)

func init() {
	registerService(serviceEntry{
		name: "azure:onlineexperimentation",
		fn:   scanOnlineExperimentation,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; the workspace ships
			// scanner-only.
			{Service: "microsoft.onlineexperimentation", DiscoType: TypeOnlineExperimentationWorkspace, Leaf: true},
		},
	})
}

// scanOnlineExperimentation discovers Azure Online Experimentation workspaces.
func scanOnlineExperimentation(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armonlineexperimentation.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armonlineexperimentation:NewWorkspacesClient: %w", err)
	}
	return azSimpleScan(ctx, "armonlineexperimentation:Workspaces.ListBySubscription", TypeOnlineExperimentationWorkspace, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armonlineexperimentation.WorkspacesClientListBySubscriptionResponse) []*armonlineexperimentation.Workspace {
			return p.Value
		},
		func(w *armonlineexperimentation.Workspace) azTrackedBase {
			return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
		})
}
