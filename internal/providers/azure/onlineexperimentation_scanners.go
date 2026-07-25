package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/onlineexperimentation/armonlineexperimentation"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOnlineExperimentationWorkspace, Service: "microsoft.onlineexperimentation", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.onlineexperimentation",
		fn:   scanOnlineExperimentation,
	})
}

// scanOnlineExperimentation discovers Azure Online Experimentation workspaces.
func scanOnlineExperimentation(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
