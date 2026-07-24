package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/webpubsub/armwebpubsub"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWebPubSub, Service: "microsoft.signalrservice", Leaf: true})
}

// scanWebPubSub discovers Azure Web PubSub resources.
func scanWebPubSub(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armwebpubsub.NewClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armwebpubsub:NewClient: %w", err)
	}
	return azSimpleScan(ctx, "armwebpubsub:WebPubSub.ListBySubscription", TypeWebPubSub, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armwebpubsub.ClientListBySubscriptionResponse) []*armwebpubsub.ResourceInfo { return p.Value },
		func(r *armwebpubsub.ResourceInfo) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
