package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/fileshares/armfileshares"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.fileshares",
		fn:   scanFileShares,
		emits: []coverage.TypeDecl{
			// Private-endpoint → target edges resolved centrally; no other
			// in-scope reference, so this ships scanner-only.
			{Service: "microsoft.fileshares", DiscoType: TypeFileSharesFileShare, Leaf: true},
		},
	})
}

// scanFileShares discovers Microsoft.FileShares managed file shares.
func scanFileShares(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armfileshares.NewClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armfileshares:NewClient: %w", err)
	}
	return azSimpleScan(ctx, "armfileshares:FileShares.ListBySubscription", TypeFileSharesFileShare, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armfileshares.ClientListBySubscriptionResponse) []*armfileshares.FileShare { return p.Value },
		func(f *armfileshares.FileShare) azTrackedBase {
			return azTrackedBase{id: sv(f.ID), name: sv(f.Name), location: sv(f.Location), tags: f.Tags, full: f}
		})
}
