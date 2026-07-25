package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/deviceprovisioningservices/armdeviceprovisioningservices"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDevicesProvisioningService, Service: "microsoft.devices", Redact: []redact.Rule{{Path: "properties.authorizationPolicies[*].primaryKey", Mode: redact.RedactScalar}, {Path: "properties.authorizationPolicies[*].secondaryKey", Mode: redact.RedactScalar}}})
}

// scanDeviceProvisioningServices discovers IoT Device Provisioning Service
// (DPS) instances. The list response embeds SAS authorization-policy keys —
// redacted via azure_redact.go.
func scanDeviceProvisioningServices(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
