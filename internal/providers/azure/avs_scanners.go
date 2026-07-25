package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/avs/armavs"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAVSPrivateCloud, Service: "microsoft.avs", Leaf: true, Redact: []redact.Rule{{Path: "properties.nsxtPassword", Mode: redact.RedactScalar}, {Path: "properties.vcenterPassword", Mode: redact.RedactScalar}, {Path: "properties.identitySources[*].password", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "azure:microsoft.avs",
		fn:   scanAVS,
	})
}

// scanAVS discovers Azure VMware Solution private clouds.
func scanAVS(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
