package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/datalake-analytics/armdatalakeanalytics"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.datalakeanalytics",
		fn:   scanDataLakeAnalytics,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.datalakeanalytics", DiscoType: TypeDataLakeAnalyticsAccount, Leaf: true},
		},
	})
}

// scanDataLakeAnalytics discovers Azure Data Lake Analytics accounts. The RP
// is deprecated (retiring) but the SDK still lists accounts; the graceful-skip
// error classifier tolerates dead-RP responses at scan time.
func scanDataLakeAnalytics(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdatalakeanalytics.NewAccountsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdatalakeanalytics:NewAccountsClient: %w", err)
	}
	return azSimpleScan(ctx, "armdatalakeanalytics:Accounts.List", TypeDataLakeAnalyticsAccount, sub, st, scanID,
		client.NewListPager(nil),
		func(p armdatalakeanalytics.AccountsClientListResponse) []*armdatalakeanalytics.AccountBasic {
			return p.Value
		},
		func(a *armdatalakeanalytics.AccountBasic) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
