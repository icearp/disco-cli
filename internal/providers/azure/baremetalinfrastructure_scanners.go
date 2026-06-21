package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/baremetalinfrastructure/armbaremetalinfrastructure"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.baremetalinfrastructure",
		fn:   scanBareMetalInfrastructure,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.baremetalinfrastructure", DiscoType: TypeBareMetalInstance, Leaf: true},
		},
	})
}

// scanBareMetalInfrastructure discovers Azure BareMetal Instances.
func scanBareMetalInfrastructure(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
