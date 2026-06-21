package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/trustedsigning/armtrustedsigning"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.codesigning",
		fn:   scanTrustedSigning,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.codesigning", DiscoType: TypeCodeSigningAccount},
		},
	})
}

// scanTrustedSigning discovers Trusted Signing (Microsoft.CodeSigning)
// code-signing accounts.
func scanTrustedSigning(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
