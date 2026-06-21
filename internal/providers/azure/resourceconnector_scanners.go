package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourceconnector/armresourceconnector"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.resourceconnector",
		fn:   scanResourceConnector,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; the appliance is the Arc
			// host root, so this ships scanner-only.
			{Service: "microsoft.resourceconnector", DiscoType: TypeResourceConnectorAppliance, Leaf: true},
		},
	})
}

// scanResourceConnector discovers Azure Arc resource bridges (appliances).
func scanResourceConnector(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armresourceconnector.NewAppliancesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armresourceconnector:NewAppliancesClient: %w", err)
	}
	return azSimpleScan(ctx, "armresourceconnector:Appliances.ListBySubscription", TypeResourceConnectorAppliance, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armresourceconnector.AppliancesClientListBySubscriptionResponse) []*armresourceconnector.Appliance {
			return p.Value
		},
		func(a *armresourceconnector.Appliance) azTrackedBase {
			return azTrackedBase{id: sv(a.ID), name: sv(a.Name), location: sv(a.Location), tags: a.Tags, full: a}
		})
}
