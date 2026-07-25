package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/healthdataaiservices/armhealthdataaiservices"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeHealthDataAIDeidService, Service: "microsoft.healthdataaiservices", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.healthdataaiservices",
		fn:   scanHealthDataAIServices,
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
