package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/connectedcache/armconnectedcache"
)

func init() {
	registerService(serviceEntry{
		name: "azure:connectedcache",
		fn:   scanConnectedCache,
		emits: []coverage.TypeDecl{
			// Enterprise / ISP MCC customer roots; their cache nodes are
			// parent-scoped (DEFER). Scanner-only.
			{Service: "microsoft.connectedcache", DiscoType: TypeConnectedCacheEnterpriseCustomer, Leaf: true},
			{Service: "microsoft.connectedcache", DiscoType: TypeConnectedCacheIspCustomer, Leaf: true},
		},
	})
}

// scanConnectedCache discovers Microsoft Connected Cache enterprise and ISP
// customer resources, both sub-wide via armconnectedcache.
func scanConnectedCache(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	emc, err := armconnectedcache.NewEnterpriseMccCustomersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconnectedcache:NewEnterpriseMccCustomersClient: %w", err)
	}
	isp, err := armconnectedcache.NewIspCustomersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconnectedcache:NewIspCustomersClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armconnectedcache:EnterpriseMccCustomers.ListBySubscription", TypeConnectedCacheEnterpriseCustomer, sub, st, scanID,
				emc.NewListBySubscriptionPager(nil),
				func(p armconnectedcache.EnterpriseMccCustomersClientListBySubscriptionResponse) []*armconnectedcache.EnterpriseMccCustomerResource {
					return p.Value
				},
				func(r *armconnectedcache.EnterpriseMccCustomerResource) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armconnectedcache:IspCustomers.ListBySubscription", TypeConnectedCacheIspCustomer, sub, st, scanID,
				isp.NewListBySubscriptionPager(nil),
				func(p armconnectedcache.IspCustomersClientListBySubscriptionResponse) []*armconnectedcache.IspCustomerResource {
					return p.Value
				},
				func(r *armconnectedcache.IspCustomerResource) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
