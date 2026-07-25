package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/certificateregistration/armcertificateregistration"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCertificateOrder, Service: "microsoft.certificateregistration", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.certificateregistration",
		fn:   scanCertificateRegistration,
	})
}

// scanCertificateRegistration discovers certificateregistration resources.
func scanCertificateRegistration(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
