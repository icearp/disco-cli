package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sqlvirtualmachine/armsqlvirtualmachine"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.sqlvirtualmachine",
		fn:   scanSQLVirtualMachine,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.sqlvirtualmachine", DiscoType: TypeSQLVirtualMachine, Leaf: true},
			{Service: "microsoft.sqlvirtualmachine", DiscoType: TypeSQLVirtualMachineGroup, Leaf: true},
		},
	})
}

// scanSQLVirtualMachine discovers SQL virtual machines and availability groups.
func scanSQLVirtualMachine(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	vms, err := armsqlvirtualmachine.NewSQLVirtualMachinesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsqlvirtualmachine:NewSQLVirtualMachinesClient: %w", err)
	}
	groups, err := armsqlvirtualmachine.NewGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsqlvirtualmachine:NewGroupsClient: %w", err)
	}
	return azRunPhases(
		func() (int, int, error) {
			return azSimpleScan(ctx, "armsqlvirtualmachine:SQLVirtualMachines.List", TypeSQLVirtualMachine, sub, st, scanID,
				vms.NewListPager(nil),
				func(p armsqlvirtualmachine.SQLVirtualMachinesClientListResponse) []*armsqlvirtualmachine.SQLVirtualMachine {
					return p.Value
				},
				func(m *armsqlvirtualmachine.SQLVirtualMachine) azTrackedBase {
					return azTrackedBase{id: sv(m.ID), name: sv(m.Name), location: sv(m.Location), tags: m.Tags, full: m}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armsqlvirtualmachine:Groups.List", TypeSQLVirtualMachineGroup, sub, st, scanID,
				groups.NewListPager(nil),
				func(p armsqlvirtualmachine.GroupsClientListResponse) []*armsqlvirtualmachine.Group { return p.Value },
				func(r *armsqlvirtualmachine.Group) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	)
}
