package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/baremetalinfrastructure/armbaremetalinfrastructure"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBareMetalInstance, Service: "microsoft.baremetalinfrastructure", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.baremetalinfrastructure",
		fn:   scanBareMetalInfrastructure,
	})
}

// scanBareMetalInfrastructure discovers Azure BareMetal Instances.
func scanBareMetalInfrastructure(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armbaremetalinfrastructure.NewAzureBareMetalInstancesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armbaremetalinfrastructure:NewAzureBareMetalInstancesClient: %w", err)
	}
	return azSimpleScan(ctx, "armbaremetalinfrastructure:AzureBareMetalInstances.ListBySubscription", TypeBareMetalInstance, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armbaremetalinfrastructure.AzureBareMetalInstancesClientListBySubscriptionResponse) []*armbaremetalinfrastructure.AzureBareMetalInstance {
			return p.Value
		},
		func(i *armbaremetalinfrastructure.AzureBareMetalInstance) azTrackedBase {
			return azTrackedBase{id: sv(i.ID), name: sv(i.Name), location: sv(i.Location), tags: i.Tags, full: i}
		})
}
