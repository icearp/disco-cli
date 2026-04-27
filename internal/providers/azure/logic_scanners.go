package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/logic/armlogic"
)

func init() { registerService(serviceEntry{name: "azure:logic", fn: scanLogic}) }

// scanLogic discovers Azure Logic Apps workflows. Triggers, actions,
// integration accounts, and API connections deferred — connection refs in
// the workflow definition are name-keyed (not ARM-IDs) and require
// per-connection resource resolution that warrants a follow-up iteration.
func scanLogic(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armlogic.NewWorkflowsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armlogic:NewWorkflowsClient: %w", err)
	}
	return azSimpleScan(ctx, "armlogic:Workflows.ListBySubscription", TypeLogicWorkflow, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armlogic.WorkflowsClientListBySubscriptionResponse) []*armlogic.Workflow { return p.Value },
		func(w *armlogic.Workflow) azTrackedBase {
			return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
		})
}
