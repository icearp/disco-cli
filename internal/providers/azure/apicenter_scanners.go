package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apicenter/armapicenter"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAPICenterService, Service: "microsoft.apicenter", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.apicenter",
		fn:   scanAPICenter,
	})
}

// scanAPICenter discovers apicenter resources.
func scanAPICenter(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armapicenter.NewServicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armapicenter:NewServicesClient: %w", err)
	}
	return azSimpleScan(ctx, "armapicenter:Services.ListBySubscription", TypeAPICenterService, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armapicenter.ServicesClientListBySubscriptionResponse) []*armapicenter.Service { return p.Value },
		func(r *armapicenter.Service) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
