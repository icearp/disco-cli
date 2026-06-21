package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/avs/armavs"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.avs",
		fn:   scanAVS,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.avs", DiscoType: TypeAVSPrivateCloud, Leaf: true},
		},
	})
}

// scanAVS discovers Azure VMware Solution private clouds.
func scanAVS(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armavs.NewPrivateCloudsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armavs:NewPrivateCloudsClient: %w", err)
	}
	return azSimpleScan(ctx, "armavs:PrivateClouds.ListInSubscription", TypeAVSPrivateCloud, sub, st, scanID,
		client.NewListInSubscriptionPager(nil),
		func(p armavs.PrivateCloudsClientListInSubscriptionResponse) []*armavs.PrivateCloud { return p.Value },
		func(c *armavs.PrivateCloud) azTrackedBase {
			return azTrackedBase{id: sv(c.ID), name: sv(c.Name), location: sv(c.Location), tags: c.Tags, full: c}
		})
}
