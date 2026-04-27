package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

func init() { registerService(serviceEntry{name: "azure:managedidentity", fn: scanManagedIdentity}) }

// scanManagedIdentity discovers Azure user-assigned managed identities.
// System-assigned identities are not standalone resources — they live as a
// principalId attribute on their host (VM, AppService, etc.) and surface to
// graph queries via the host's role assignments.
func scanManagedIdentity(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmsi.NewUserAssignedIdentitiesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmsi:NewUserAssignedIdentitiesClient: %w", err)
	}
	return azPageScan(ctx, "armmsi:UserAssignedIdentities.ListBySubscription", sub, st,
		client.NewListBySubscriptionPager(nil),
		func(page armmsi.UserAssignedIdentitiesClientListBySubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			var pairs [][2]string
			for _, id := range page.Value {
				if id == nil || id.ID == nil {
					continue
				}
				name, loc := sv(id.Name), sv(id.Location)
				nativeID := sv(id.ID)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeManagedIdentityUserAssigned, NativeID: nativeID,
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(id.Tags), AttributesJSON: mustJSON(id),
					DiscoveredBy: scanID,
				})
				if rgFromID(nativeID) != "" {
					pairs = append(pairs, rgHierarchyPair(sub, TypeManagedIdentityUserAssigned, nativeID))
				}
			}
			return batch, pairs
		})
}
