package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/batch/armbatch"
)

func init() {
	registerService(serviceEntry{
		name: "azure:batch",
		fn:   scanBatch,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.batch", DiscoType: TypeBatchAccount},
		},
	})
}

// scanBatch discovers Azure Batch accounts.
func scanBatch(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armbatch.NewAccountClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armbatch:NewAccountClient: %w", err)
	}
	return azSimpleScan(ctx, "armbatch:Account.List", TypeBatchAccount, sub, st, scanID,
		client.NewListPager(nil),
		func(p armbatch.AccountClientListResponse) []*armbatch.Account { return p.Value },
		func(a *armbatch.Account) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
