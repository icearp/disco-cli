package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/automation/armautomation"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAutomationAccount, Service: "microsoft.automation"})
	registerService(serviceEntry{
		name: "azure:microsoft.automation",
		fn:   scanAutomation,
	})
}

// scanAutomation discovers Azure Automation accounts.
func scanAutomation(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
