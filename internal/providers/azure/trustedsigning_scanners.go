package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/trustedsigning/armtrustedsigning"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeSigningAccount, Service: "microsoft.codesigning"})
	registerService(serviceEntry{
		name: "azure:microsoft.codesigning",
		fn:   scanTrustedSigning,
	})
}

// scanTrustedSigning discovers Trusted Signing (Microsoft.CodeSigning)
// code-signing accounts.
func scanTrustedSigning(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armtrustedsigning.NewCodeSigningAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armtrustedsigning:NewCodeSigningAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armtrustedsigning:CodeSigningAccounts.ListBySubscription", TypeCodeSigningAccount, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armtrustedsigning.CodeSigningAccountsClientListBySubscriptionResponse) []*armtrustedsigning.CodeSigningAccount {
			return p.Value
		},
		func(r *armtrustedsigning.CodeSigningAccount) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
