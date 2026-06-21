package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/healthdataaiservices/armhealthdataaiservices"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.healthdataaiservices",
		fn:   scanHealthDataAIServices,
		emits: []coverage.TypeDecl{
			// Identity → MSI and private-endpoint edges resolved centrally; the
			// de-identification service ships scanner-only.
			{Service: "microsoft.healthdataaiservices", DiscoType: TypeHealthDataAIDeidService, Leaf: true},
		},
	})
}

// scanHealthDataAIServices discovers Health Data AI Services de-identification services.
func scanHealthDataAIServices(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhealthdataaiservices.NewDeidServicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhealthdataaiservices:NewDeidServicesClient: %w", err)
	}
	return azSimpleScan(ctx, "armhealthdataaiservices:DeidServices.ListBySubscription", TypeHealthDataAIDeidService, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armhealthdataaiservices.DeidServicesClientListBySubscriptionResponse) []*armhealthdataaiservices.DeidService {
			return p.Value
		},
		func(d *armhealthdataaiservices.DeidService) azTrackedBase {
			return azTrackedBase{id: sv(d.ID), name: sv(d.Name), location: sv(d.Location), tags: d.Tags, full: d}
		})
}
