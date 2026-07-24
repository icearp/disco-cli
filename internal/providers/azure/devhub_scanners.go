package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devhub/armdevhub"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDevHubWorkflow, Service: "microsoft.devhub", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.devhub",
		fn:   scanDevHub,
	})
}

// scanDevHub discovers devhub resources.
func scanDevHub(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdevhub.NewWorkflowClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdevhub:NewWorkflowClient: %w", err)
	}
	return azSimpleScan(ctx, "armdevhub:Workflow.List", TypeDevHubWorkflow, sub, st, scanID,
		client.NewListPager(nil),
		func(p armdevhub.WorkflowClientListResponse) []*armdevhub.Workflow { return p.Value },
		func(r *armdevhub.Workflow) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
