package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/durabletask/armdurabletask"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDurableTaskScheduler, Service: "microsoft.durabletask", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.durabletask",
		fn:   scanDurableTask,
	})
}

// scanDurableTask discovers durabletask resources.
func scanDurableTask(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armdurabletask.NewSchedulersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdurabletask:NewSchedulersClient: %w", err)
	}
	return azSimpleScan(ctx, "armdurabletask:Schedulers.ListBySubscription", TypeDurableTaskScheduler, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armdurabletask.SchedulersClientListBySubscriptionResponse) []*armdurabletask.Scheduler {
			return p.Value
		},
		func(r *armdurabletask.Scheduler) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
