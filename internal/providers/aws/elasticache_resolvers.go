package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveElastiCacheRelationships) }

// resolveElastiCacheRelationships orchestrates all ElastiCache relationship resolution.
func resolveElastiCacheRelationships(acct *account, st *store.Store) error {
	// Build lookup maps once and reuse across resolvers.
	rgMap, err := buildElastiCacheRGMap(acct, st)
	if err != nil {
		return err
	}
	sgMap, err := buildElastiCacheSubnetGroupMap(acct, st)
	if err != nil {
		return err
	}
	pgMap, err := buildElastiCacheParameterGroupMap(acct, st)
	if err != nil {
		return err
	}
	ugMap, err := buildElastiCacheUserGroupMap(acct, st)
	if err != nil {
		return err
	}

	if err := resolveClusterToReplicationGroup(acct, st, rgMap); err != nil {
		return err
	}
	if err := resolveClusterToSubnetGroup(acct, st, sgMap); err != nil {
		return err
	}
	if err := resolveClusterToParameterGroup(acct, st, pgMap); err != nil {
		return err
	}
	if err := resolveReplicationGroupToSubnetGroup(acct, st, sgMap); err != nil {
		return err
	}
	if err := resolveReplicationGroupToUserGroups(acct, st, ugMap); err != nil {
		return err
	}
	if err := resolveGlobalRGToReplicationGroups(acct, st, rgMap); err != nil {
		return err
	}
	if err := resolveUserGroupToUsers(acct, st); err != nil {
		return err
	}
	return resolveServerlessCacheToUserGroup(acct, st, ugMap)
}

// --- lookup map builders ---

// buildElastiCacheRGMap returns a map of ReplicationGroupId → store resource ID.
func buildElastiCacheRGMap(acct *account, st *store.Store) (map[string]string, error) {
	rgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheReplicationGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rgs))
	for _, r := range rgs {
		var attrs struct {
			ReplicationGroupId *string `json:"ReplicationGroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ReplicationGroupId != nil {
			m[*attrs.ReplicationGroupId] = r.ID
		}
	}
	return m, nil
}

// buildElastiCacheSubnetGroupMap returns a map of CacheSubnetGroupName → store resource ID.
func buildElastiCacheSubnetGroupMap(acct *account, st *store.Store) (map[string]string, error) {
	sgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheSubnetGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(sgs))
	for _, r := range sgs {
		var attrs struct {
			CacheSubnetGroupName *string `json:"CacheSubnetGroupName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.CacheSubnetGroupName != nil {
			m[*attrs.CacheSubnetGroupName] = r.ID
		}
	}
	return m, nil
}

// buildElastiCacheParameterGroupMap returns a map of CacheParameterGroupName → store resource ID.
func buildElastiCacheParameterGroupMap(acct *account, st *store.Store) (map[string]string, error) {
	pgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheParameterGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(pgs))
	for _, r := range pgs {
		var attrs struct {
			CacheParameterGroupName *string `json:"CacheParameterGroupName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.CacheParameterGroupName != nil {
			m[*attrs.CacheParameterGroupName] = r.ID
		}
	}
	return m, nil
}

// buildElastiCacheUserGroupMap returns a map of UserGroupId → store resource ID.
func buildElastiCacheUserGroupMap(acct *account, st *store.Store) (map[string]string, error) {
	ugs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheUserGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(ugs))
	for _, r := range ugs {
		var attrs struct {
			UserGroupId *string `json:"UserGroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.UserGroupId != nil {
			m[*attrs.UserGroupId] = r.ID
		}
	}
	return m, nil
}

// --- resolver functions ---

// resolveClusterToReplicationGroup links each cache cluster (Redis) to its parent
// replication group. Memcached clusters have no ReplicationGroupId and are skipped.
func resolveClusterToReplicationGroup(acct *account, st *store.Store, rgMap map[string]string) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheCacheCluster},
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
			continue
		}
		rgID, ok := rgMap[*attrs.ReplicationGroupId]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, rgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cluster→replication-group: %w", err)
		}
	}
	return nil
}

// resolveClusterToSubnetGroup links each cache cluster to its subnet group.
func resolveClusterToSubnetGroup(acct *account, st *store.Store, sgMap map[string]string) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheCacheCluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range clusters {
		var attrs struct {
			CacheSubnetGroupName *string `json:"CacheSubnetGroupName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.CacheSubnetGroupName == nil || *attrs.CacheSubnetGroupName == "" {
			continue
		}
		sgID, ok := sgMap[*attrs.CacheSubnetGroupName]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, sgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cluster→subnet-group: %w", err)
		}
	}
	return nil
}

// resolveClusterToParameterGroup links each cache cluster to its parameter group.
func resolveClusterToParameterGroup(acct *account, st *store.Store, pgMap map[string]string) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheCacheCluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range clusters {
		// CacheParameterGroup is a nested object on the cluster.
		var attrs struct {
			CacheParameterGroup *struct {
				CacheParameterGroupName *string `json:"CacheParameterGroupName"`
			} `json:"CacheParameterGroup"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.CacheParameterGroup == nil || attrs.CacheParameterGroup.CacheParameterGroupName == nil {
			continue
		}
		pgID, ok := pgMap[*attrs.CacheParameterGroup.CacheParameterGroupName]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, pgID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert cluster→parameter-group: %w", err)
		}
	}
	return nil
}

// resolveReplicationGroupToSubnetGroup links each replication group to its subnet group.
func resolveReplicationGroupToSubnetGroup(acct *account, st *store.Store, sgMap map[string]string) error {
	rgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheReplicationGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rgs {
		var attrs struct {
			CacheSubnetGroupName *string `json:"CacheSubnetGroupName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.CacheSubnetGroupName == nil || *attrs.CacheSubnetGroupName == "" {
			continue
		}
		sgID, ok := sgMap[*attrs.CacheSubnetGroupName]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, sgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert replication-group→subnet-group: %w", err)
		}
	}
	return nil
}

// resolveReplicationGroupToUserGroups links each replication group to its associated user groups.
func resolveReplicationGroupToUserGroups(acct *account, st *store.Store, ugMap map[string]string) error {
	rgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheReplicationGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rgs {
		var attrs struct {
			UserGroupIds []string `json:"UserGroupIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, ugID := range attrs.UserGroupIds {
			storeID, ok := ugMap[ugID]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, storeID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert replication-group→user-group: %w", err)
			}
		}
	}
	return nil
}

// resolveGlobalRGToReplicationGroups links each global replication group to its
// member replication groups in this account.
func resolveGlobalRGToReplicationGroups(acct *account, st *store.Store, rgMap map[string]string) error {
	grgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheGlobalReplicationGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range grgs {
		var attrs struct {
			Members []struct {
				ReplicationGroupId *string `json:"ReplicationGroupId"`
			} `json:"Members"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, m := range attrs.Members {
			if m.ReplicationGroupId == nil {
				continue
			}
			rgID, ok := rgMap[*m.ReplicationGroupId]
			if !ok {
				continue // member may belong to a different account
			}
			if err := st.UpsertRelationship(r.ID, rgID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert global-replication-group→replication-group: %w", err)
			}
		}
	}
	return nil
}

// resolveUserGroupToUsers links each user group to its member users.
func resolveUserGroupToUsers(acct *account, st *store.Store) error {
	// Build a map of UserId → store resource ID.
	users, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheUser},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	userMap := make(map[string]string, len(users))
	for _, r := range users {
		var attrs struct {
			UserId *string `json:"UserId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.UserId != nil {
			userMap[*attrs.UserId] = r.ID
		}
	}

	ugs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheUserGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range ugs {
		var attrs struct {
			UserIds []string `json:"UserIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, uid := range attrs.UserIds {
			userID, ok := userMap[uid]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, userID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert user-group→user: %w", err)
			}
		}
	}
	return nil
}

// resolveServerlessCacheToUserGroup links each serverless cache to its user group.
func resolveServerlessCacheToUserGroup(acct *account, st *store.Store, ugMap map[string]string) error {
	scs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeElastiCacheServerlessCache},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range scs {
		var attrs struct {
			UserGroupId *string `json:"UserGroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.UserGroupId == nil || *attrs.UserGroupId == "" {
			continue
		}
		ugID, ok := ugMap[*attrs.UserGroupId]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, ugID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert serverless-cache→user-group: %w", err)
		}
	}
	return nil
}
