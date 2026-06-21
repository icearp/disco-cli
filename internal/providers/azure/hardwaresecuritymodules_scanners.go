package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hardwaresecuritymodules/armhardwaresecuritymodules"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.hardwaresecuritymodules",
		fn:   scanHardwareSecurityModules,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.hardwaresecuritymodules", DiscoType: TypeDedicatedHsm, Leaf: true},
		},
	})
}

// scanHardwareSecurityModules discovers hardwaresecuritymodules resources.
func scanHardwareSecurityModules(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
