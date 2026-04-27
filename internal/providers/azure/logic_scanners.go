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
	return azPageScan(ctx, "armlogic:Workflows.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armlogic.WorkflowsClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
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
					Type: TypeLogicWorkflow, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(w.Tags), AttributesJSON: mustJSON(w),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeLogicWorkflow, nativeID))
				}
			}
			return batch, pairs
		})
}
