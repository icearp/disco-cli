package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance"
)

func init() {
	registerService(serviceEntry{
		name: "azure:containerapps",
		fn:   scanContainerApps,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.app", DiscoType: TypeAppContainersManagedEnvironment},
			{Service: "microsoft.app", DiscoType: TypeAppContainersContainerApp},
			{Service: "microsoft.containerinstance", DiscoType: TypeContainerInstanceContainerGroup},
		},
	})
}

// scanContainerApps discovers Azure Container Apps managed environments + apps
// and Azure Container Instances container groups. Three resource types in one
// service: environment is the parent for ContainerApp; ACI groups are
// independent.
func scanContainerApps(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	envClient, err := armappcontainers.NewManagedEnvironmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armappcontainers:NewManagedEnvironmentsClient: %w", err)
	}
	et, ei, err := azSimpleScan(ctx, "armappcontainers:ManagedEnvironments.ListBySubscription", TypeAppContainersManagedEnvironment, sub, st, scanID,
		envClient.NewListBySubscriptionPager(nil),
		func(p armappcontainers.ManagedEnvironmentsClientListBySubscriptionResponse) []*armappcontainers.ManagedEnvironment {
			return p.Value
		},
		func(r *armappcontainers.ManagedEnvironment) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
	total += et
	inserted += ei
	if err != nil {
		return total, inserted, err
	}

	appClient, err := armappcontainers.NewContainerAppsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armappcontainers:NewContainerAppsClient: %w", err)
	}
	at, ai, err := azSimpleScan(ctx, "armappcontainers:ContainerApps.ListBySubscription", TypeAppContainersContainerApp, sub, st, scanID,
		appClient.NewListBySubscriptionPager(nil),
		func(p armappcontainers.ContainerAppsClientListBySubscriptionResponse) []*armappcontainers.ContainerApp {
			return p.Value
		},
		func(r *armappcontainers.ContainerApp) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
	total += at
	inserted += ai
	if err != nil {
		return total, inserted, err
	}

	aciClient, err := armcontainerinstance.NewContainerGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armcontainerinstance:NewContainerGroupsClient: %w", err)
	}
	gt, gi, err := azSimpleScan(ctx, "armcontainerinstance:ContainerGroups.List", TypeContainerInstanceContainerGroup, sub, st, scanID,
		aciClient.NewListPager(nil),
		func(p armcontainerinstance.ContainerGroupsClientListResponse) []*armcontainerinstance.ContainerGroup {
			return p.Value
		},
		func(r *armcontainerinstance.ContainerGroup) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
	total += gt
	inserted += gi
	return total, inserted, err
}
