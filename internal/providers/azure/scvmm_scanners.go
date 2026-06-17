package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/scvmm/armscvmm"
)

func init() {
	registerService(serviceEntry{
		name: "azure:scvmm",
		fn:   scanScVmm,
		emits: []coverage.TypeDecl{
			// Custom-location edge wired centrally; the VMM server is the
			// Arc-SCVMM root, so this ships scanner-only.
			{Service: "microsoft.scvmm", DiscoType: TypeScVmmServer, Leaf: true},
		},
	})
}

// scanScVmm discovers Arc-connected System Center VMM servers.
func scanScVmm(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armscvmm.NewVmmServersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armscvmm:NewVmmServersClient: %w", err)
	}
	return azSimpleScan(ctx, "armscvmm:VmmServers.ListBySubscription", TypeScVmmServer, sub, st, scanID,
		client.NewListBySubscriptionPager(nil),
		func(p armscvmm.VmmServersClientListBySubscriptionResponse) []*armscvmm.VmmServer { return p.Value },
		func(s *armscvmm.VmmServer) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
}
