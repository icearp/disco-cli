package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/deviceprovisioningservices/armdeviceprovisioningservices"
)

func init() {
	registerService(serviceEntry{
		name: "azure:deviceprovisioningservices",
		fn:   scanDeviceProvisioningServices,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.devices", DiscoType: TypeDevicesProvisioningService},
		},
	})
}

// scanDeviceProvisioningServices discovers IoT Device Provisioning Service
// (DPS) instances. The list response embeds SAS authorization-policy keys —
// redacted via azure_redact.go.
func scanDeviceProvisioningServices(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdeviceprovisioningservices.NewIotDpsResourceClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdeviceprovisioningservices:NewIotDpsResourceClient: %w", err)
	}
	return azSimpleScan(ctx, "armdeviceprovisioningservices:IotDpsResource.ListBySubscription", TypeDevicesProvisioningService, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdeviceprovisioningservices.IotDpsResourceClientListBySubscriptionResponse) []*armdeviceprovisioningservices.ProvisioningServiceDescription {
			return p.Value
		},
		func(r *armdeviceprovisioningservices.ProvisioningServiceDescription) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
