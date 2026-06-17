package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devcenter/armdevcenter"
)

func init() {
	registerService(serviceEntry{
		name: "azure:devcenter",
		fn:   scanDevCenter,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.devcenter", DiscoType: TypeDevCenter, Leaf: true},
		},
	})
}

// scanDevCenter discovers devcenter resources.
func scanDevCenter(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdevcenter.NewDevCentersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdevcenter:NewDevCentersClient: %w", err)
	}
	return azSimpleScan(ctx, "armdevcenter:DevCenters.ListBySubscription", TypeDevCenter, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdevcenter.DevCentersClientListBySubscriptionResponse) []*armdevcenter.DevCenter {
			return p.Value
		},
		func(r *armdevcenter.DevCenter) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
