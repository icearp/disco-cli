package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dependencymap/armdependencymap"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDependencyMapMap, Service: "microsoft.dependencymap"})
	registerService(serviceEntry{
		name: "azure:microsoft.dependencymap",
		fn:   scanDependencyMap,
	})
}

// scanDependencyMap discovers Azure Dependency Map resources.
func scanDependencyMap(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdependencymap.NewMapsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdependencymap:NewMapsClient: %w", err)
	}
	return azSimpleScan(ctx, "armdependencymap:Maps.ListBySubscription", TypeDependencyMapMap, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdependencymap.MapsClientListBySubscriptionResponse) []*armdependencymap.MapsResource {
			return p.Value
		},
		func(r *armdependencymap.MapsResource) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
