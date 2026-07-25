package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/devtestlabs/armdevtestlabs"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDevTestLabSchedule, Service: "microsoft.devtestlab"})
}

// scanDevTestLabs discovers DevTest Labs global schedules (subscription-wide
// auto-shutdown/start). Labs, VMs, and artifacts are parent-scoped and
// deferred.
func scanDevTestLabs(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdevtestlabs.NewGlobalSchedulesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdevtestlabs:NewGlobalSchedulesClient: %w", err)
	}
	return azSimpleScan(ctx, "armdevtestlabs:GlobalSchedules.ListBySubscription", TypeDevTestLabSchedule, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdevtestlabs.GlobalSchedulesClientListBySubscriptionResponse) []*armdevtestlabs.Schedule {
			return p.Value
		},
		func(r *armdevtestlabs.Schedule) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
