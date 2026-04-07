package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveElastiCacheRelationships) }

// resolveElastiCacheRelationships links each cache cluster to its parent
// replication group when ReplicationGroupId is set (Redis only; Memcached
// clusters have no replication group).
func resolveElastiCacheRelationships(acct *account, st *store.Store) error {
	// Build a map of replication group ID → store resource ID.
	rgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheReplicationGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	rgIDToStoreID := make(map[string]string, len(rgs))
	for _, rg := range rgs {
		var attrs struct {
			ReplicationGroupId *string `json:"ReplicationGroupId"`
		}
		if err := json.Unmarshal([]byte(rg.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ReplicationGroupId != nil {
			rgIDToStoreID[*attrs.ReplicationGroupId] = rg.ID
		}
	}

	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheCluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range clusters {
		var attrs struct {
			ReplicationGroupId *string `json:"ReplicationGroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ReplicationGroupId == nil || *attrs.ReplicationGroupId == "" {
			continue // Memcached cluster — no parent replication group
		}
		rgStoreID, ok := rgIDToStoreID[*attrs.ReplicationGroupId]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, rgStoreID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert elasticache cluster→replication-group: %w", err)
		}
	}
	return nil
}
