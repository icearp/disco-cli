package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveElastiCacheSnapshotRelationships,
		EdgeDecl{TypeElastiCacheSnapshot, TypeElastiCacheCacheCluster, store.RelAttachedTo},
		EdgeDecl{TypeElastiCacheSnapshot, TypeElastiCacheReplicationGroup, store.RelAttachedTo},
		EdgeDecl{TypeElastiCacheSnapshot, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveElastiCacheServerlessSnapshotRelationships,
		EdgeDecl{TypeElastiCacheServerlessCacheSnapshot, TypeElastiCacheServerlessCache, store.RelAttachedTo},
		EdgeDecl{TypeElastiCacheServerlessCacheSnapshot, TypeKMSKey, store.RelUses},
	)
}

// ecByName indexes scanned rows of one type by their Name (ElastiCache snapshots
// reference clusters/groups by id/name, while those rows are ARN-keyed).
func ecByName(acct *account, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		if r.Name != nil && *r.Name != "" {
			idx[*r.Name] = r.ID
		}
	}
	return idx, nil
}

func resolveElastiCacheSnapshotRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeElastiCacheSnapshot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusterByName, err := ecByName(acct, st, TypeElastiCacheCacheCluster)
	if err != nil {
		return err
	}
	rgByName, err := ecByName(acct, st, TypeElastiCacheReplicationGroup)
	if err != nil {
		return err
	}
	kms, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			CacheClusterID     *string `json:"CacheClusterId"`
			ReplicationGroupID *string `json:"ReplicationGroupId"`
			KmsKeyID           *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id, ok := clusterByName[sv(attrs.CacheClusterID)]; ok {
			if err := st.UpsertRelationship(r.ID, id, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert elasticache snapshot→cache-cluster: %w", err)
			}
		}
		if id, ok := rgByName[sv(attrs.ReplicationGroupID)]; ok {
			if err := st.UpsertRelationship(r.ID, id, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert elasticache snapshot→replication-group: %w", err)
			}
		}
		if ref := sv(attrs.KmsKeyID); ref != "" {
			if keyID, ok := kms.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert elasticache snapshot→kms: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveElastiCacheServerlessSnapshotRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeElastiCacheServerlessCacheSnapshot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scByName, err := ecByName(acct, st, TypeElastiCacheServerlessCache)
	if err != nil {
		return err
	}
	kms, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ServerlessCacheConfiguration *struct {
				ServerlessCacheName *string `json:"ServerlessCacheName"`
			} `json:"ServerlessCacheConfiguration"`
			KmsKeyID *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ServerlessCacheConfiguration != nil {
			if id, ok := scByName[sv(attrs.ServerlessCacheConfiguration.ServerlessCacheName)]; ok {
				if err := st.UpsertRelationship(r.ID, id, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert elasticache serverless-snapshot→serverless-cache: %w", err)
				}
			}
		}
		if ref := sv(attrs.KmsKeyID); ref != "" {
			if keyID, ok := kms.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert elasticache serverless-snapshot→kms: %w", err)
				}
			}
		}
	}
	return nil
}
