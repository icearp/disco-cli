package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/certificateregistration/armcertificateregistration"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.certificateregistration",
		fn:   scanCertificateRegistration,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.certificateregistration", DiscoType: TypeCertificateOrder, Leaf: true},
		},
	})
}

// scanCertificateRegistration discovers certificateregistration resources.
func scanCertificateRegistration(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcertificateregistration.NewAppServiceCertificateOrdersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcertificateregistration:NewAppServiceCertificateOrdersClient: %w", err)
	}
	return azSimpleScan(ctx, "armcertificateregistration:AppServiceCertificateOrders.List", TypeCertificateOrder, sub, st, scanID,
		client.NewListPager(nil),
		func(p armcertificateregistration.AppServiceCertificateOrdersClientListResponse) []*armcertificateregistration.AppServiceCertificateOrder {
			return p.Value
		},
		func(r *armcertificateregistration.AppServiceCertificateOrder) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
