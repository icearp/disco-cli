package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveDatabaseWatcherRelationships,
		EdgeDecl{Source: TypeDatabaseWatcher, Target: TypeKustoCluster, Kind: store.RelUses},
	)
}

// resolveDatabaseWatcherRelationships wires a Database Watcher to its backing
// Azure Data Explorer cluster via properties.datastore.adxClusterResourceId
// (full Microsoft.Kusto/clusters ARM ID). kustoClusterUri (the required
// generic Kusto endpoint) is ARM-addressable only when adxClusterResourceId
// is also set (free-offering clusters have no ARM resource to link to), so
// the ARM-ID match is the complete in-scope edge.
func resolveDatabaseWatcherRelationships(sub *subscription, st *store.Store) error {
	watchers, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeDatabaseWatcher},
		Limit: util.AllResources,
	})
	if err != nil || len(watchers) == 0 {
		return err
	}
	kustoByID, err := nativeIDIndex(sub, st, TypeKustoCluster)
	if err != nil {
		return err
	}
	for _, w := range watchers {
		var attrs struct {
			Properties *struct {
				Datastore *struct {
					AdxClusterResourceID *string `json:"adxClusterResourceId"`
				} `json:"datastore"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(w.AttributesJSON), &attrs); err != nil ||
			attrs.Properties == nil || attrs.Properties.Datastore == nil {
			continue
		}
		if ref := attrs.Properties.Datastore.AdxClusterResourceID; ref != nil {
			if toID, ok := kustoByID[strings.ToLower(*ref)]; ok {
				if err := st.UpsertRelationship(w.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert databasewatcher→kusto: %w", err)
				}
			}
		}
	}
	return nil
}
