package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/connectedvmware/armconnectedvmware"
)

func init() {
	registerService(serviceEntry{
		name: "azure:connectedvmware",
		fn:   scanConnectedVMware,
		emits: []coverage.TypeDecl{
			// Custom-location edge wired centrally; the vCenter is the Arc-VMware
			// root and carries no other in-scope reference.
			{Service: "microsoft.connectedvmwarevsphere", DiscoType: TypeConnectedVMwareVCenter, Leaf: true},
		},
	})
}

// scanConnectedVMware discovers Arc-connected VMware vCenters.
func scanConnectedVMware(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armconnectedvmware.NewVCentersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armconnectedvmware:NewVCentersClient: %w", err)
	}
	return azSimpleScan(ctx, "armconnectedvmware:VCenters.List", TypeConnectedVMwareVCenter, sub, st, scanID,
		client.NewListPager(nil),
		func(p armconnectedvmware.VCentersClientListResponse) []*armconnectedvmware.VCenter { return p.Value },
		func(v *armconnectedvmware.VCenter) azTrackedBase {
			return azTrackedBase{id: sv(v.ID), name: sv(v.Name), location: sv(v.Location), tags: v.Tags, full: v}
		})
}
