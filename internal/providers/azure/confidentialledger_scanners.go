package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/confidentialledger/armconfidentialledger"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConfidentialLedger, Service: "microsoft.confidentialledger", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.confidentialledger",
		fn:   scanConfidentialLedger,
	})
}

// scanConfidentialLedger discovers confidentialledger resources.
func scanConfidentialLedger(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
