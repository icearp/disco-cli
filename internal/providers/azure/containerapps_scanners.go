package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance"
)

func init() { registerService(serviceEntry{name: "azure:containerapps", fn: scanContainerApps}) }

// scanContainerApps discovers Azure Container Apps managed environments + apps
// and Azure Container Instances container groups. Three resource types in one
// service: environment is the parent for ContainerApp; ACI groups are
// independent.
func scanContainerApps(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	envClient, err := armappcontainers.NewManagedEnvironmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armappcontainers:NewManagedEnvironmentsClient: %w", err)
	}
	et, ei, err := azPageScan(ctx, "armappcontainers:ManagedEnvironments.ListBySubscription", sub, st,
		envClient.NewListBySubscriptionPager(nil),
		func(page armappcontainers.ManagedEnvironmentsClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, r := range page.Value {
				if r == nil || r.ID == nil {
					continue
				}
				name, loc := sv(r.Name), sv(r.Location)
				nativeID := sv(r.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeAppContainersManagedEnvironment, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(r.Tags), AttributesJSON: mustJSON(r),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeAppContainersManagedEnvironment, nativeID))
				}
			}
			return batch, pairs
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
	at, ai, err := azPageScan(ctx, "armappcontainers:ContainerApps.ListBySubscription", sub, st,
		appClient.NewListBySubscriptionPager(nil),
		func(page armappcontainers.ContainerAppsClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, r := range page.Value {
				if r == nil || r.ID == nil {
					continue
				}
				name, loc := sv(r.Name), sv(r.Location)
				nativeID := sv(r.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeAppContainersContainerApp, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(r.Tags), AttributesJSON: mustJSON(r),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeAppContainersContainerApp, nativeID))
				}
			}
			return batch, pairs
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
	gt, gi, err := azPageScan(ctx, "armcontainerinstance:ContainerGroups.List", sub, st,
		aciClient.NewListPager(nil),
		func(page armcontainerinstance.ContainerGroupsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, r := range page.Value {
				if r == nil || r.ID == nil {
					continue
				}
				name, loc := sv(r.Name), sv(r.Location)
				nativeID := sv(r.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeContainerInstanceContainerGroup, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(r.Tags), AttributesJSON: mustJSON(r),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeContainerInstanceContainerGroup, nativeID))
				}
			}
			return batch, pairs
		})
	total += gt
	inserted += gi
	return total, inserted, err
}
