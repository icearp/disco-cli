package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
)

func init() {
	registerType(restype.Descriptor{Type: TypeManagedIdentityUserAssigned, Service: "microsoft.managedidentity"})
	registerService(serviceEntry{
		name: "azure:microsoft.managedidentity",
		fn:   scanManagedIdentity,
	})
}

// scanManagedIdentity discovers Azure user-assigned managed identities.
// System-assigned identities are not standalone resources — they live as a
// principalId attribute on their host (VM, AppService, etc.) and surface to
// graph queries via the host's role assignments.
func scanManagedIdentity(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
