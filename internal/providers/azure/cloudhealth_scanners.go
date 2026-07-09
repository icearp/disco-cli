package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cloudhealth/armcloudhealth"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCloudHealthHealthModel, Service: "microsoft.cloudhealth"})
	registerService(serviceEntry{
		name: "azure:microsoft.cloudhealth",
		fn:   scanCloudHealth,
	})
}

// scanCloudHealth discovers Azure Cloud Health health models.
func scanCloudHealth(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcloudhealth.NewHealthModelsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcloudhealth:NewHealthModelsClient: %w", err)
	}
	return azSimpleScan(ctx, "armcloudhealth:HealthModels.ListBySubscription", TypeCloudHealthHealthModel, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armcloudhealth.HealthModelsClientListBySubscriptionResponse) []*armcloudhealth.HealthModel {
			return p.Value
		},
		func(r *armcloudhealth.HealthModel) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
