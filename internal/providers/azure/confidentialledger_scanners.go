package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/confidentialledger/armconfidentialledger"
)

func init() {
	registerService(serviceEntry{
		name: "azure:confidentialledger",
		fn:   scanConfidentialLedger,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.confidentialledger", DiscoType: TypeConfidentialLedger, Leaf: true},
		},
	})
}

// scanConfidentialLedger discovers confidentialledger resources.
func scanConfidentialLedger(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armconfidentialledger.NewLedgerClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconfidentialledger:NewLedgerClient: %w", err)
	}
	return azSimpleScan(ctx, "armconfidentialledger:Ledger.ListBySubscription", TypeConfidentialLedger, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armconfidentialledger.LedgerClientListBySubscriptionResponse) []*armconfidentialledger.ConfidentialLedger {
			return p.Value
		},
		func(r *armconfidentialledger.ConfidentialLedger) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
