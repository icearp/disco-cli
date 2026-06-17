package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sqlvirtualmachine/armsqlvirtualmachine"
)

func init() {
	registerService(serviceEntry{
		name: "azure:sqlvirtualmachine",
		fn:   scanSQLVirtualMachine,
		emits: []coverage.TypeDecl{
			// Identity → MSI edges resolved centrally; the SQL VM ships
			// scanner-only.
			{Service: "microsoft.sqlvirtualmachine", DiscoType: TypeSQLVirtualMachine, Leaf: true},
		},
	})
}

// scanSQLVirtualMachine discovers SQL Server on Azure VMs (SQL IaaS extension registrations).
func scanSQLVirtualMachine(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armsqlvirtualmachine.NewSQLVirtualMachinesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsqlvirtualmachine:NewSQLVirtualMachinesClient: %w", err)
	}
	return azSimpleScan(ctx, "armsqlvirtualmachine:SQLVirtualMachines.List", TypeSQLVirtualMachine, sub, st, scanID,
		client.NewListPager(nil),
		func(p armsqlvirtualmachine.SQLVirtualMachinesClientListResponse) []*armsqlvirtualmachine.SQLVirtualMachine {
			return p.Value
		},
		func(m *armsqlvirtualmachine.SQLVirtualMachine) azTrackedBase {
			return azTrackedBase{id: sv(m.ID), name: sv(m.Name), location: sv(m.Location), tags: m.Tags, full: m}
		})
}
