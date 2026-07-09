package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/loadtesting/armloadtesting"
)

func init() {
	registerType(restype.Descriptor{Type: TypeLoadTest, Service: "microsoft.loadtestservice", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.loadtestservice",
		fn:   scanLoadTesting,
	})
}

// scanLoadTesting discovers Azure Load Testing resources.
func scanLoadTesting(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armloadtesting.NewLoadTestsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armloadtesting:NewLoadTestsClient: %w", err)
	}
	return azSimpleScan(ctx, "armloadtesting:LoadTests.ListBySubscription", TypeLoadTest, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armloadtesting.LoadTestsClientListBySubscriptionResponse) []*armloadtesting.LoadTestResource {
			return p.Value
		},
		func(r *armloadtesting.LoadTestResource) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
