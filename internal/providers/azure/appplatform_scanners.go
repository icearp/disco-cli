package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appplatform/armappplatform"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppPlatformService, Service: "microsoft.appplatform"})
	registerService(serviceEntry{
		name: "azure:microsoft.appplatform",
		fn:   scanAppPlatform,
	})
}

// scanAppPlatform discovers Azure Spring Apps service instances.
func scanAppPlatform(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armappplatform.NewServicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armappplatform:NewServicesClient: %w", err)
	}
	return azSimpleScan(ctx, "armappplatform:Services.ListBySubscription", TypeAppPlatformService, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armappplatform.ServicesClientListBySubscriptionResponse) []*armappplatform.ServiceResource {
			return p.Value
		},
		func(s *armappplatform.ServiceResource) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
}
