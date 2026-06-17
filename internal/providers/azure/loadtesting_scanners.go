package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/loadtesting/armloadtesting"
)

func init() {
	registerService(serviceEntry{
		name: "azure:loadtestservice",
		fn:   scanLoadTesting,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; CMK is referenced by key
			// URI (preserved by omission), so the load test ships scanner-only.
			{Service: "microsoft.loadtestservice", DiscoType: TypeLoadTest, Leaf: true},
		},
	})
}

// scanLoadTesting discovers Azure Load Testing resources.
func scanLoadTesting(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
