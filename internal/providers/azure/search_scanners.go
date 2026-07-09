package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/search/armsearch"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSearchService, Service: "microsoft.search", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.search",
		fn:   scanSearch,
	})
}

// scanSearch discovers Azure AI Search services.
func scanSearch(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armsearch.NewServicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsearch:NewServicesClient: %w", err)
	}
	return azSimpleScan(ctx, "armsearch:Services.ListBySubscription", TypeSearchService, sub, st, scanID,
		client.NewListBySubscriptionPager(nil, nil),
		func(p armsearch.ServicesClientListBySubscriptionResponse) []*armsearch.Service { return p.Value },
		func(s *armsearch.Service) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
}
