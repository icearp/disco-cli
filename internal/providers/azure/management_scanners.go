package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managementgroups/armmanagementgroups"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
)

func init() { registerService(serviceEntry{name: "azure:management", fn: scanManagement}) }

// scanManagement discovers tenant-scoped governance entities: Azure Management
// Groups and the subscription itself as a first-class resource. Both are
// tenant-scoped APIs but the scanner runs per-subscription (duplication
// accepted — same precedent as RBAC built-in role-definitions; ResourceID
// hash includes account_id so per-sub resolvers FK locally).
//
// Adding subscription-as-resource closes a gap for scope-attached resolvers:
// policy assignments scoped to `/subscriptions/<id>` could not previously FK
// to a stored resource; they now will.
func scanManagement(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	mgClient, err := armmanagementgroups.NewClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagementgroups:NewClient: %w", err)
	}
	mt, mi, err := azPageScan(ctx, "armmanagementgroups:List", sub, st,
		mgClient.NewListPager(nil),
		func(page armmanagementgroups.ClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, mg := range page.Value {
				if mg == nil || mg.ID == nil {
					continue
				}
				name := sv(mg.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeManagementGroup, NativeID: sv(mg.ID),
					Name:           &name,
					AttributesJSON: mustJSON(mg),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
	total += mt
	inserted += mi
	if err != nil {
		return total, inserted, err
	}

	subClient, err := armsubscription.NewSubscriptionsClient(cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armsubscription:NewSubscriptionsClient: %w", err)
	}
	st1, si1, err := azPageScan(ctx, "armsubscription:Subscriptions.List", sub, st,
		subClient.NewListPager(nil),
		func(page armsubscription.SubscriptionsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, s := range page.Value {
				if s == nil || s.ID == nil {
					continue
				}
				name := sv(s.DisplayName)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeSubscription, NativeID: sv(s.ID),
					Name:           &name,
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
	total += st1
	inserted += si1
	return total, inserted, err
}
