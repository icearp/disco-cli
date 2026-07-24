package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning/v4"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMachineLearningWorkspace, Service: "microsoft.machinelearningservices"})
	registerType(restype.Descriptor{Type: TypeMachineLearningRegistry, Service: "microsoft.machinelearningservices", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.machinelearningservices",
		fn:   scanMachineLearning,
	})
}

// scanMachineLearning discovers Azure Machine Learning workspaces and registries.
func scanMachineLearning(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	workspaces, err := armmachinelearning.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmachinelearning:NewWorkspacesClient: %w", err)
	}
	registries, err := armmachinelearning.NewRegistriesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmachinelearning:NewRegistriesClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmachinelearning:Workspaces.ListBySubscription", TypeMachineLearningWorkspace, sub, st, scanID,
				workspaces.NewListBySubscriptionPager(nil),
				func(p armmachinelearning.WorkspacesClientListBySubscriptionResponse) []*armmachinelearning.Workspace {
					return p.Value
				},
				func(w *armmachinelearning.Workspace) azTrackedBase {
					return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmachinelearning:Registries.ListBySubscription", TypeMachineLearningRegistry, sub, st, scanID,
				registries.NewListBySubscriptionPager(nil),
				func(p armmachinelearning.RegistriesClientListBySubscriptionResponse) []*armmachinelearning.Registry {
					return p.Value
				},
				func(r *armmachinelearning.Registry) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
