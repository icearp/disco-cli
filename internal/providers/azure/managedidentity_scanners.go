package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.managedidentity",
		fn:   scanManagedIdentity,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.managedidentity", DiscoType: TypeManagedIdentityUserAssigned},
		},
	})
}

// scanManagedIdentity discovers Azure user-assigned managed identities.
// System-assigned identities are not standalone resources — they live as a
// principalId attribute on their host (VM, AppService, etc.) and surface to
// graph queries via the host's role assignments.
func scanManagedIdentity(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armmsi.NewUserAssignedIdentitiesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmsi:NewUserAssignedIdentitiesClient: %w", err)
	}
	return azSimpleScan(ctx, "armmsi:UserAssignedIdentities.ListBySubscription", TypeManagedIdentityUserAssigned, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armmsi.UserAssignedIdentitiesClientListBySubscriptionResponse) []*armmsi.Identity {
			return p.Value
		},
		func(id *armmsi.Identity) azTrackedBase {
			return azTrackedBase{id: sv(id.ID), name: sv(id.Name), location: sv(id.Location), tags: id.Tags, full: id}
		})
}
