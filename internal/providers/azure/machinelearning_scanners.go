package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v4"
)

func init() {
	registerService(serviceEntry{
		name: "azure:machinelearning",
		fn:   scanMachineLearning,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.machinelearningservices", DiscoType: TypeMachineLearningWorkspace},
		},
	})
}

// scanMachineLearning discovers Azure Machine Learning workspaces.
func scanMachineLearning(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmachinelearning.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmachinelearning:NewWorkspacesClient: %w", err)
	}
	return azSimpleScan(ctx, "armmachinelearning:Workspaces.ListBySubscription", TypeMachineLearningWorkspace, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armmachinelearning.WorkspacesClientListBySubscriptionResponse) []*armmachinelearning.Workspace {
			return p.Value
		},
		func(w *armmachinelearning.Workspace) azTrackedBase {
			return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
		})
}
