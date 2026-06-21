package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/webpubsub/armwebpubsub"
)

func init() {
	registerExtraEmits([]coverage.TypeDecl{
		// Identity → MSI and private-endpoint edges resolve centrally.
		{Service: "microsoft.signalrservice", DiscoType: TypeWebPubSub, Leaf: true},
	}...)
}

// scanWebPubSub discovers Azure Web PubSub resources.
func scanWebPubSub(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
