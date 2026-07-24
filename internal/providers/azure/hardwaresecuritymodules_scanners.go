package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hardwaresecuritymodules/armhardwaresecuritymodules"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDedicatedHsm, Service: "microsoft.hardwaresecuritymodules", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.hardwaresecuritymodules",
		fn:   scanHardwareSecurityModules,
	})
}

// scanHardwareSecurityModules discovers hardwaresecuritymodules resources.
func scanHardwareSecurityModules(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armhardwaresecuritymodules.NewDedicatedHsmClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armhardwaresecuritymodules:NewDedicatedHsmClient: %w", err)
	}
	return azSimpleScan(ctx, "armhardwaresecuritymodules:DedicatedHsm.ListBySubscription", TypeDedicatedHsm, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armhardwaresecuritymodules.DedicatedHsmClientListBySubscriptionResponse) []*armhardwaresecuritymodules.DedicatedHsm {
			return p.Value
		},
		func(r *armhardwaresecuritymodules.DedicatedHsm) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
