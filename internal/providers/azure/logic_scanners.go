package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/logic/armlogic"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeLogicWorkflow, Service: "microsoft.logic"})
	registerType(restype.Descriptor{Type: TypeLogicIntegrationAccount, Service: "microsoft.logic"})
	registerType(restype.Descriptor{Type: TypeLogicIntegrationServiceEnv, Service: "microsoft.logic"})
	registerService(serviceEntry{
		name: "azure:microsoft.logic",
		fn:   scanLogic,
	})
}

// scanLogic discovers Azure Logic Apps workflows, integration accounts, and
// integration service environments. Triggers, actions, and API connections are
// deferred: workflow-definition connection refs are name-keyed (not ARM IDs)
// and need per-connection resolution — a follow-up.
func scanLogic(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			wfClient, err := armlogic.NewWorkflowsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armlogic:NewWorkflowsClient: %w", err)
			}
			return azSimpleScan(ctx, "armlogic:Workflows.ListBySubscription", TypeLogicWorkflow, sub, st, scanID,
				wfClient.NewListBySubscriptionPager(nil),
				func(p armlogic.WorkflowsClientListBySubscriptionResponse) []*armlogic.Workflow { return p.Value },
				func(w *armlogic.Workflow) azTrackedBase {
					return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
				})
		},
		func() (int, int, error) {
			iaClient, err := armlogic.NewIntegrationAccountsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armlogic:NewIntegrationAccountsClient: %w", err)
			}
			return azSimpleScan(ctx, "armlogic:IntegrationAccounts.ListBySubscription", TypeLogicIntegrationAccount, sub, st, scanID,
				iaClient.NewListBySubscriptionPager(nil),
				func(p armlogic.IntegrationAccountsClientListBySubscriptionResponse) []*armlogic.IntegrationAccount {
					return p.Value
				},
				func(r *armlogic.IntegrationAccount) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			iseClient, err := armlogic.NewIntegrationServiceEnvironmentsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armlogic:NewIntegrationServiceEnvironmentsClient: %w", err)
			}
			return azSimpleScan(ctx, "armlogic:IntegrationServiceEnvironments.ListBySubscription", TypeLogicIntegrationServiceEnv, sub, st, scanID,
				iseClient.NewListBySubscriptionPager(nil),
				func(p armlogic.IntegrationServiceEnvironmentsClientListBySubscriptionResponse) []*armlogic.IntegrationServiceEnvironment {
					return p.Value
				},
				func(r *armlogic.IntegrationServiceEnvironment) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
