package azure

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/databasewatcher/armdatabasewatcher"
	"github.com/icearp/disco-cli/store"
)

// TestResolveDatabaseWatcherRelationships verifies a watcher derives a -[uses]->
// Kusto cluster edge from properties.datastore.adxClusterResourceId, matched
// case-insensitively.
func TestResolveDatabaseWatcherRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)

	kustoNativeID := "/subscriptions/" + testSubID + "/resourceGroups/rg/providers/Microsoft.Kusto/clusters/adx"
	kustoID := upsertTestResource(t, st, "azure", sub.ID, TypeKustoCluster, kustoNativeID, "eastus", "{}")

	w := armdatabasewatcher.Watcher{
		Properties: &armdatabasewatcher.WatcherProperties{
			Datastore: &armdatabasewatcher.Datastore{
				// Mixed case vs lowercase stored cluster exercises the probe ToLower.
				AdxClusterResourceID: to.Ptr("/subscriptions/" + testSubID + "/resourceGroups/RG/providers/Microsoft.Kusto/clusters/ADX"),
			},
		},
	}
	wID := upsertTestResource(t, st, "azure", sub.ID, TypeDatabaseWatcher,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.DatabaseWatcher/watchers/w", "eastus", marshalAttrs(t, w))

	if err := resolveDatabaseWatcherRelationships(sub, st); err != nil {
		t.Fatalf("resolveDatabaseWatcherRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(wID)
	if len(rels) != 1 || rels[0].ToID != kustoID || rels[0].Kind != store.RelUses {
		t.Errorf("expected one -[uses]-> kusto edge, got %+v", rels)
	}
}

// TestResolveDatabaseWatcherRelationships_NoRefs verifies a watcher with no
// datastore produces no edges and does not panic.
func TestResolveDatabaseWatcherRelationships_NoRefs(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription(testSubID)
	wID := upsertTestResource(t, st, "azure", sub.ID, TypeDatabaseWatcher,
		"/subscriptions/"+testSubID+"/resourceGroups/rg/providers/Microsoft.DatabaseWatcher/watchers/w", "eastus", "{}")
	if err := resolveDatabaseWatcherRelationships(sub, st); err != nil {
		t.Fatalf("resolveDatabaseWatcherRelationships: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(wID); len(rels) != 0 {
		t.Errorf("expected no edges, got %+v", rels)
	}
}
