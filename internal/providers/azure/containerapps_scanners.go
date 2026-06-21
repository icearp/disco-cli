package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.app",
		fn:   scanContainerApps,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.app", DiscoType: TypeAppContainersManagedEnvironment},
			{Service: "microsoft.app", DiscoType: TypeAppContainersContainerApp},
			{Service: "microsoft.app", DiscoType: TypeAppContainersConnectedEnvironment},
			{Service: "microsoft.app", DiscoType: TypeAppContainersJob},
			{Service: "microsoft.app", DiscoType: TypeAppContainersSessionPool},
		},
	})
	registerService(serviceEntry{
		name: "azure:microsoft.containerinstance",
		fn:   scanContainerInstance,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.containerinstance", DiscoType: TypeContainerInstanceContainerGroup},
		},
	})
}

// scanContainerApps discovers Microsoft.App resources — Container Apps managed
// environments + apps, connected environments, jobs, session pools.
// Microsoft.App/builders has no subscription-wide list op in the SDK and is
// omitted.
func scanContainerApps(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			envClient, err := armappcontainers.NewManagedEnvironmentsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armappcontainers:NewManagedEnvironmentsClient: %w", err)
			}
			return azSimpleScan(ctx, "armappcontainers:ManagedEnvironments.ListBySubscription", TypeAppContainersManagedEnvironment, sub, st, scanID,
				envClient.NewListBySubscriptionPager(nil),
				func(p armappcontainers.ManagedEnvironmentsClientListBySubscriptionResponse) []*armappcontainers.ManagedEnvironment {
					return p.Value
				},
				func(r *armappcontainers.ManagedEnvironment) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			appClient, err := armappcontainers.NewContainerAppsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armappcontainers:NewContainerAppsClient: %w", err)
			}
			return azSimpleScan(ctx, "armappcontainers:ContainerApps.ListBySubscription", TypeAppContainersContainerApp, sub, st, scanID,
				appClient.NewListBySubscriptionPager(nil),
				func(p armappcontainers.ContainerAppsClientListBySubscriptionResponse) []*armappcontainers.ContainerApp {
					return p.Value
				},
				func(r *armappcontainers.ContainerApp) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			connEnvClient, err := armappcontainers.NewConnectedEnvironmentsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armappcontainers:NewConnectedEnvironmentsClient: %w", err)
			}
			return azSimpleScan(ctx, "armappcontainers:ConnectedEnvironments.ListBySubscription", TypeAppContainersConnectedEnvironment, sub, st, scanID,
				connEnvClient.NewListBySubscriptionPager(nil),
				func(p armappcontainers.ConnectedEnvironmentsClientListBySubscriptionResponse) []*armappcontainers.ConnectedEnvironment {
					return p.Value
				},
				func(r *armappcontainers.ConnectedEnvironment) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			jobsClient, err := armappcontainers.NewJobsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armappcontainers:NewJobsClient: %w", err)
			}
			return azSimpleScan(ctx, "armappcontainers:Jobs.ListBySubscription", TypeAppContainersJob, sub, st, scanID,
				jobsClient.NewListBySubscriptionPager(nil),
				func(p armappcontainers.JobsClientListBySubscriptionResponse) []*armappcontainers.Job { return p.Value },
				func(r *armappcontainers.Job) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			poolClient, err := armappcontainers.NewContainerAppsSessionPoolsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armappcontainers:NewContainerAppsSessionPoolsClient: %w", err)
			}
			return azSimpleScan(ctx, "armappcontainers:SessionPools.ListBySubscription", TypeAppContainersSessionPool, sub, st, scanID,
				poolClient.NewListBySubscriptionPager(nil),
				func(p armappcontainers.ContainerAppsSessionPoolsClientListBySubscriptionResponse) []*armappcontainers.SessionPool {
					return p.Value
				},
				func(r *armappcontainers.SessionPool) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}

// scanContainerInstance discovers Azure Container Instances container groups
// (Microsoft.ContainerInstance).
func scanContainerInstance(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	aciClient, err := armcontainerinstance.NewContainerGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcontainerinstance:NewContainerGroupsClient: %w", err)
	}
	return azSimpleScan(ctx, "armcontainerinstance:ContainerGroups.List", TypeContainerInstanceContainerGroup, sub, st, scanID,
		aciClient.NewListPager(nil),
		func(p armcontainerinstance.ContainerGroupsClientListResponse) []*armcontainerinstance.ContainerGroup {
			return p.Value
		},
		func(r *armcontainerinstance.ContainerGroup) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
