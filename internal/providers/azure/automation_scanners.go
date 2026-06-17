package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/automation/armautomation"
)

func init() {
	registerService(serviceEntry{
		name: "azure:automation",
		fn:   scanAutomation,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.automation", DiscoType: TypeAutomationAccount},
		},
	})
}

// scanAutomation discovers Azure Automation accounts.
func scanAutomation(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armautomation.NewAccountClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armautomation:NewAccountClient: %w", err)
	}
	return azSimpleScan(ctx, "armautomation:Account.List", TypeAutomationAccount, sub, st, scanID,
		client.NewListPager(nil),
		func(p armautomation.AccountClientListResponse) []*armautomation.Account { return p.Value },
		func(a *armautomation.Account) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
