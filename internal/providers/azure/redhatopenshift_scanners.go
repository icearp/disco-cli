package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redhatopenshift/armredhatopenshift"
)

func init() {
	registerService(serviceEntry{
		name: "azure:redhatopenshift",
		fn:   scanRedHatOpenShift,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.redhatopenshift", DiscoType: TypeOpenShiftCluster, Leaf: true},
		},
	})
}

// scanRedHatOpenShift discovers redhatopenshift resources.
func scanRedHatOpenShift(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armredhatopenshift.NewOpenShiftClustersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armredhatopenshift:NewOpenShiftClustersClient: %w", err)
	}
	return azSimpleScan(ctx, "armredhatopenshift:OpenShiftClusters.List", TypeOpenShiftCluster, sub, st, scanID,
		client.NewListPager(nil),
		func(p armredhatopenshift.OpenShiftClustersClientListResponse) []*armredhatopenshift.OpenShiftCluster {
			return p.Value
		},
		func(r *armredhatopenshift.OpenShiftCluster) azTrackedBase {
			return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
		})
}
