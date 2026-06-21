package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationsmanagement/armoperationsmanagement"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.operationsmanagement",
		fn:   scanOperationsManagement,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.operationsmanagement", DiscoType: TypeOpsManagementSolution, Leaf: true},
		},
	})
}

// scanOperationsManagement discovers Azure Operations Management (OMS)
// solutions. ListBySubscription is a single call (no pager), so it is wrapped
// inline rather than via azSimpleScan.
func scanOperationsManagement(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armoperationsmanagement.NewSolutionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armoperationsmanagement:NewSolutionsClient: %w", err)
	}
	resp, err := client.ListBySubscription(ctx, nil)
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "armoperationsmanagement:Solutions.ListBySubscription", sub.ID, err)
		}
		return 0, 0, fmt.Errorf("armoperationsmanagement:Solutions.ListBySubscription: %w", err)
	}
	batch, pairs := azTrackedRows(sub, scanID, TypeOpsManagementSolution, resp.Value,
		func(s *armoperationsmanagement.Solution) azTrackedBase {
			return azTrackedBase{id: sv(s.ID), name: sv(s.Name), location: sv(s.Location), tags: s.Tags, full: s}
		})
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert OperationsManagementSolutions: %w", err)
	}
	if len(pairs) > 0 {
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return len(batch), n, fmt.Errorf("closure OperationsManagementSolutions: %w", err)
		}
	}
	return len(batch), n, nil
}
