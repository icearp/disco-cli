package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storagediscovery/armstoragediscovery"
)

func init() {
	registerType(restype.Descriptor{Type: TypeStorageDiscoveryWorkspace, Service: "microsoft.storagediscovery", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.storagediscovery",
		fn:   scanStorageDiscovery,
	})
}

// scanStorageDiscovery discovers Azure Storage Discovery workspaces.
func scanStorageDiscovery(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armstoragediscovery.NewWorkspacesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armstoragediscovery:NewWorkspacesClient: %w", err)
	}
	return azSimpleScan(ctx, "armstoragediscovery:Workspaces.ListBySubscription", TypeStorageDiscoveryWorkspace, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armstoragediscovery.WorkspacesClientListBySubscriptionResponse) []*armstoragediscovery.Workspace {
			return p.Value
		},
		func(w *armstoragediscovery.Workspace) azTrackedBase {
			return azTrackedBase{id: sv(w.ID), name: sv(w.Name), location: sv(w.Location), tags: w.Tags, full: w}
		})
}
