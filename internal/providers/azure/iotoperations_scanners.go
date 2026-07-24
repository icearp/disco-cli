package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/iotoperations/armiotoperations"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTOperationsInstance, Service: "microsoft.iotoperations", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.iotoperations",
		fn:   scanIoTOperations,
	})
}

// scanIoTOperations discovers Azure IoT Operations instances.
func scanIoTOperations(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armiotoperations.NewInstanceClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armiotoperations:NewInstanceClient: %w", err)
	}
	return azSimpleScan(ctx, "armiotoperations:Instance.ListBySubscription", TypeIoTOperationsInstance, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armiotoperations.InstanceClientListBySubscriptionResponse) []*armiotoperations.InstanceResource {
			return p.Value
		},
		func(i *armiotoperations.InstanceResource) azTrackedBase {
			return azTrackedBase{id: sv(i.ID), name: sv(i.Name), location: sv(i.Location), tags: i.Tags, full: i}
		})
}
