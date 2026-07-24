package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/blueprint/armblueprint"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBlueprintBlueprint, Service: "microsoft.blueprint"})
	registerType(restype.Descriptor{Type: TypeBlueprintAssignment, Service: "microsoft.blueprint"})
	registerService(serviceEntry{
		name: "azure:microsoft.blueprint",
		fn:   scanBlueprint,
	})
}

// scanBlueprint discovers Azure Blueprints definitions and assignments. Both
// list ops are scope-wide; disco scans at subscription scope (blueprints can
// also live at management-group scope, out of per-sub reach — deferred).
// Blueprint definitions are proxy resources without location/tags.
func scanBlueprint(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	scope := "/subscriptions/" + sub.ID
	return azRunPhases(
		func() (int, int, error) {
			bpClient, err := armblueprint.NewBlueprintsClient(cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armblueprint:NewBlueprintsClient: %w", err)
			}
			return azSimpleScan(ctx, "armblueprint:Blueprints.List", TypeBlueprintBlueprint, sub, st, scanID,
				bpClient.NewListPager(scope, nil),
				func(p armblueprint.BlueprintsClientListResponse) []*armblueprint.Blueprint { return p.Value },
				func(r *armblueprint.Blueprint) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), full: r}
				})
		},
		func() (int, int, error) {
			asClient, err := armblueprint.NewAssignmentsClient(cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armblueprint:NewAssignmentsClient: %w", err)
			}
			return azSimpleScan(ctx, "armblueprint:Assignments.List", TypeBlueprintAssignment, sub, st, scanID,
				asClient.NewListPager(scope, nil),
				func(p armblueprint.AssignmentsClientListResponse) []*armblueprint.Assignment { return p.Value },
				func(r *armblueprint.Assignment) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), full: r}
				})
		},
	)
}
