package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveElastiCacheRelationships,
		EdgeDecl{TypeElastiCacheCacheCluster, TypeElastiCacheReplicationGroup, store.RelAttachedTo},
		EdgeDecl{TypeElastiCacheCacheCluster, TypeElastiCacheSubnetGroup, store.RelAttachedTo},
		EdgeDecl{TypeElastiCacheCacheCluster, TypeElastiCacheParameterGroup, store.RelUses},
		EdgeDecl{TypeElastiCacheReplicationGroup, TypeElastiCacheSubnetGroup, store.RelAttachedTo},
		EdgeDecl{TypeElastiCacheReplicationGroup, TypeElastiCacheUserGroup, store.RelAttachedTo},
		EdgeDecl{TypeElastiCacheGlobalReplicationGroup, TypeElastiCacheReplicationGroup, store.RelContains},
		EdgeDecl{TypeElastiCacheUserGroup, TypeElastiCacheUser, store.RelContains},
		EdgeDecl{TypeElastiCacheServerlessCache, TypeElastiCacheUserGroup, store.RelAttachedTo},
	)
}

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

// buildElastiCacheRGMap returns a map of ReplicationGroupID → store resource ID.
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
			ReplicationGroupID *string `json:"ReplicationGroupID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ReplicationGroupID != nil {
			m[*attrs.ReplicationGroupID] = r.ID
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

// buildElastiCacheUserGroupMap returns a map of UserGroupID → store resource ID.
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
			UserGroupID *string `json:"UserGroupID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.UserGroupID != nil {
			m[*attrs.UserGroupID] = r.ID
		}
	}
	return m, nil
}

// --- resolver functions ---

// resolveClusterToReplicationGroup links each cache cluster (Redis) to its parent
// replication group. Memcached clusters have no ReplicationGroupID and are skipped.
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
			ReplicationGroupID *string `json:"ReplicationGroupID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ReplicationGroupID == nil || *attrs.ReplicationGroupID == "" {
			continue
		}
		rgID, ok := rgMap[*attrs.ReplicationGroupID]
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
			UserGroupIDs []string `json:"UserGroupIDs"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, ugID := range attrs.UserGroupIDs {
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
				ReplicationGroupID *string `json:"ReplicationGroupID"`
			} `json:"Members"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, m := range attrs.Members {
			if m.ReplicationGroupID == nil {
				continue
			}
			rgID, ok := rgMap[*m.ReplicationGroupID]
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
	// Build a map of UserID → store resource ID.
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
			UserID *string `json:"UserID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.UserID != nil {
			userMap[*attrs.UserID] = r.ID
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
			UserIDs []string `json:"UserIDs"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, uid := range attrs.UserIDs {
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
			UserGroupID *string `json:"UserGroupID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.UserGroupID == nil || *attrs.UserGroupID == "" {
			continue
		}
		ugID, ok := ugMap[*attrs.UserGroupID]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, ugID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert serverless-cache→user-group: %w", err)
		}
	}
	return nil
}
