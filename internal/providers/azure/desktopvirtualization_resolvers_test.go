package azure

import (
	"testing"

	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/desktopvirtualization/armdesktopvirtualization/v2"
)

// TestResolveDesktopVirtualizationRelationships verifies the AVD object graph:
// application-group → host-pool, workspace → application-group, and
// scaling-plan → host-pool, all matched case-insensitively on ARM IDs.
func TestResolveDesktopVirtualizationRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	pfx := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.DesktopVirtualization/"
	hpNativeID := pfx + "hostPools/HP1"
	hpID := upsertTestResource(t, st, "azure", sub.ID, TypeDVCHostPool, hpNativeID, "eastus", "{}")
	agNativeID := pfx + "applicationGroups/AG1"

	ag := armdesktopvirtualization.ApplicationGroup{
		Properties: &armdesktopvirtualization.ApplicationGroupProperties{
			// Upper-cased ref exercises the lowercased index.
			HostPoolArmPath: to.Ptr(upper(hpNativeID)),
		},
	}
	agID := upsertTestResource(t, st, "azure", sub.ID, TypeDVCApplicationGroup, agNativeID, "eastus", marshalAttrs(t, ag))

	ws := armdesktopvirtualization.Workspace{
		Properties: &armdesktopvirtualization.WorkspaceProperties{
			ApplicationGroupReferences: []*string{to.Ptr(upper(agNativeID))},
		},
	}
	wsID := upsertTestResource(t, st, "azure", sub.ID, TypeDVCWorkspace, pfx+"workspaces/WS1", "eastus", marshalAttrs(t, ws))

	sp := armdesktopvirtualization.ScalingPlan{
		Properties: &armdesktopvirtualization.ScalingPlanProperties{
			HostPoolReferences: []*armdesktopvirtualization.ScalingHostPoolReference{
				{HostPoolArmPath: to.Ptr(upper(hpNativeID))},
				{HostPoolArmPath: to.Ptr(upper(hpNativeID))}, // dup → single edge
			},
		},
	}
	spID := upsertTestResource(t, st, "azure", sub.ID, TypeDVCScalingPlan, pfx+"scalingPlans/SP1", "eastus", marshalAttrs(t, sp))

	if err := resolveDesktopVirtualizationRelationships(sub, st); err != nil {
		t.Fatalf("resolveDesktopVirtualizationRelationships: %v", err)
	}

	if rels, _ := st.RelationshipsFrom(agID); len(rels) != 1 || rels[0].ToID != hpID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected appgroup -[attached-to]-> hostpool, got %+v", rels)
	}
	if rels, _ := st.RelationshipsFrom(wsID); len(rels) != 1 || rels[0].ToID != agID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected workspace -[attached-to]-> appgroup, got %+v", rels)
	}
	if rels, _ := st.RelationshipsFrom(spID); len(rels) != 1 || rels[0].ToID != hpID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected scalingplan -[attached-to]-> hostpool (deduped), got %+v", rels)
	}
}

// TestResolveDesktopVirtualizationRelationships_NoRefs verifies empty-attrs
// resources across all three source types produce no edges and no panic.
func TestResolveDesktopVirtualizationRelationships_NoRefs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	pfx := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.DesktopVirtualization/"
	upsertTestResource(t, st, "azure", sub.ID, TypeDVCHostPool, pfx+"hostPools/HP1", "eastus", "{}")
	agID := upsertTestResource(t, st, "azure", sub.ID, TypeDVCApplicationGroup, pfx+"applicationGroups/AG1", "eastus", "{}")
	wsID := upsertTestResource(t, st, "azure", sub.ID, TypeDVCWorkspace, pfx+"workspaces/WS1", "eastus", "{}")
	spID := upsertTestResource(t, st, "azure", sub.ID, TypeDVCScalingPlan, pfx+"scalingPlans/SP1", "eastus", "{}")

	if err := resolveDesktopVirtualizationRelationships(sub, st); err != nil {
		t.Fatalf("resolveDesktopVirtualizationRelationships: %v", err)
	}
	for _, id := range []string{agID, wsID, spID} {
		if rels, _ := st.RelationshipsFrom(id); len(rels) != 0 {
			t.Errorf("expected no edges for %s, got %+v", id, rels)
		}
	}
}
