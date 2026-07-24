package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devcenter/armdevcenter"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDevCenter, Service: "microsoft.devcenter", Leaf: true})
	registerType(restype.Descriptor{Type: TypeDevCenterProject, Service: "microsoft.devcenter", Leaf: true})
	registerType(restype.Descriptor{Type: TypeDevCenterNetworkConnection, Service: "microsoft.devcenter", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.devcenter",
		fn:   scanDevCenter,
	})
}

// scanDevCenter discovers Dev Centers, projects, and network connections.
func scanDevCenter(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	centers, err := armdevcenter.NewDevCentersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdevcenter:NewDevCentersClient: %w", err)
	}
	projects, err := armdevcenter.NewProjectsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdevcenter:NewProjectsClient: %w", err)
	}
	conns, err := armdevcenter.NewNetworkConnectionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdevcenter:NewNetworkConnectionsClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdevcenter:DevCenters.ListBySubscription", TypeDevCenter, sub, st, scanID,
				centers.NewListBySubscriptionPager(nil),
				func(p armdevcenter.DevCentersClientListBySubscriptionResponse) []*armdevcenter.DevCenter {
					return p.Value
				},
				func(r *armdevcenter.DevCenter) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdevcenter:Projects.ListBySubscription", TypeDevCenterProject, sub, st, scanID,
				projects.NewListBySubscriptionPager(nil),
				func(p armdevcenter.ProjectsClientListBySubscriptionResponse) []*armdevcenter.Project { return p.Value },
				func(r *armdevcenter.Project) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdevcenter:NetworkConnections.ListBySubscription", TypeDevCenterNetworkConnection, sub, st, scanID,
				conns.NewListBySubscriptionPager(nil),
				func(p armdevcenter.NetworkConnectionsClientListBySubscriptionResponse) []*armdevcenter.NetworkConnection {
					return p.Value
				},
				func(r *armdevcenter.NetworkConnection) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
