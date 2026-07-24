package azure

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/edgemarketplace/armedgemarketplace"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEdgeMarketplaceOffer, Service: "microsoft.edgemarketplace", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEdgeMarketplacePublisher, Service: "microsoft.edgemarketplace", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.edgemarketplace",
		fn:   scanEdgeMarketplace,
	})
}

// scanEdgeMarketplace discovers Edge Marketplace offers + publishers, both
// proxy resources (no location / tags).
func scanEdgeMarketplace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	offersClient, err := armedgemarketplace.NewOffersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armedgemarketplace:NewOffersClient: %w", err)
	}
	publishersClient, err := armedgemarketplace.NewPublishersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armedgemarketplace:NewPublishersClient: %w", err)
	}

	var (
		mu       sync.Mutex
		firstErr error
	)
	addTotals := func(t, n int, e error) {
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}

	phases := []func() (int, int, error){
		func() (int, int, error) {
			return azSimpleScan(ctx, "armedgemarketplace:Offers.ListBySubscription", TypeEdgeMarketplaceOffer, sub, st, scanID,
				offersClient.NewListBySubscriptionPager(nil),
				func(p armedgemarketplace.OffersClientListBySubscriptionResponse) []*armedgemarketplace.Offer {
					return p.Value
				},
				func(r *armedgemarketplace.Offer) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armedgemarketplace:Publishers.ListBySubscription", TypeEdgeMarketplacePublisher, sub, st, scanID,
				publishersClient.NewListBySubscriptionPager(nil),
				func(p armedgemarketplace.PublishersClientListBySubscriptionResponse) []*armedgemarketplace.Publisher {
					return p.Value
				},
				func(r *armedgemarketplace.Publisher) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), full: r}
				})
		},
	}

	var wg sync.WaitGroup
	for _, fn := range phases {
		wg.Go(func() {
			t, n, e := fn()
			addTotals(t, n, e)
		})
	}
	wg.Wait()
	return total, inserted, firstErr
}
