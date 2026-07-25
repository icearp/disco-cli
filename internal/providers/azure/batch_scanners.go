package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/batch/armbatch"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBatchAccount, Service: "microsoft.batch"})
	registerService(serviceEntry{
		name: "azure:microsoft.batch",
		fn:   scanBatch,
	})
}

// scanBatch discovers Azure Batch accounts.
func scanBatch(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
