package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/synapse/armsynapse"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSynapseWorkspace, Service: "microsoft.synapse"})
	registerType(restype.Descriptor{Type: TypeSynapsePrivateLinkHub, Service: "microsoft.synapse", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.synapse",
		fn:   scanSynapse,
	})
}

// scanSynapse discovers Azure Synapse Analytics workspaces and private link
// hubs. SQL/Spark pools, integration runtimes, private endpoint connections,
// and managed private endpoints deferred — their graph value lives in
// workspace-level edges (default ADLS, managed VNet, CMEK).
func scanSynapse(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			client, err := armsynapse.NewWorkspacesClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armsynapse:NewWorkspacesClient: %w", err)
			}
			return azSimpleScan(ctx, "armsynapse:Workspaces.List", TypeSynapseWorkspace, sub, st, scanID,
				client.NewListPager(nil),
				func(p armsynapse.WorkspacesClientListResponse) []*armsynapse.Workspace { return p.Value },
				func(w *armsynapse.Workspace) azTrackedBase {
					return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
				})
		},
		func() (int, int, error) {
			plhClient, err := armsynapse.NewPrivateLinkHubsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armsynapse:NewPrivateLinkHubsClient: %w", err)
			}
			return azSimpleScan(ctx, "armsynapse:PrivateLinkHubs.List", TypeSynapsePrivateLinkHub, sub, st, scanID,
				plhClient.NewListPager(nil),
				func(p armsynapse.PrivateLinkHubsClientListResponse) []*armsynapse.PrivateLinkHub { return p.Value },
				func(h *armsynapse.PrivateLinkHub) azTrackedBase {
					return azTrackedBase{id: sv(h.ID), name: sv(h.Name), location: sv(h.Location), tags: h.Tags, full: h}
				})
		},
	)
}
