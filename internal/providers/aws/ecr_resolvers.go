package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveECRRepositoryRelationships,
		EdgeDecl{TypeECRRepository, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveECRReplicationConfiguration,
		EdgeDecl{TypeECRReplicationConfiguration, TypeECRRepository, store.RelUses},
	)
}

// resolveECRRepositoryRelationships links each ECR repository to the KMS key
// that encrypts its image layers when encryption type is KMS. AES256
// encryption uses AWS-owned keys that disco doesn't scan.
func resolveECRRepositoryRelationships(acct *account, st *store.Store) error {
	repos, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeECRRepository},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range repos {
		var attrs struct {
			EncryptionConfiguration *struct {
				EncryptionType *string `json:"EncryptionType"`
				KmsKey         *string `json:"KmsKey"`
			} `json:"EncryptionConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EncryptionConfiguration == nil || sv(attrs.EncryptionConfiguration.KmsKey) == "" {
			continue
		}
		keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, *attrs.EncryptionConfiguration.KmsKey)
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ecr→kms relationship: %w", err)
		}
	}
	return nil
}

// resolveECRReplicationConfiguration emits one edge per (replication-config,
// matched repo) pair. A rule with no RepositoryFilters replicates the whole
// registry; a PREFIX_MATCH filter restricts to repos whose name starts with
// the given prefix. Source-side only — destinations are (RegistryId, Region)
// tuples, not repo ARNs, so there is no concrete target to point at without a
// stub-resource pass.
func resolveECRReplicationConfiguration(acct *account, st *store.Store) error {
	configs, err := st.ListResources(store.ResourceFilter{
		Providers:      []string{"aws"},
		AccountID:      acct.ID,
		Types:          []string{TypeECRReplicationConfiguration},
		IncludeManaged: true,
		Limit:          util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(configs) == 0 {
		return nil
	}
	repos, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeECRRepository},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	// region → repos. Replication source repos live in the same region as the
	// registry's replication-configuration row.
	reposByRegion := make(map[string][]store.Resource, len(repos))
	for _, r := range repos {
		reg := sv(r.Region)
		reposByRegion[reg] = append(reposByRegion[reg], r)
	}
	for _, cfg := range configs {
		var attrs struct {
			Rules []struct {
				RepositoryFilters []struct {
					Filter     *string `json:"Filter"`
					FilterType *string `json:"FilterType"`
				} `json:"RepositoryFilters"`
			} `json:"Rules"`
		}
		if err := json.Unmarshal([]byte(cfg.AttributesJSON), &attrs); err != nil {
			continue
		}
		regionRepos := reposByRegion[sv(cfg.Region)]
		if len(regionRepos) == 0 || len(attrs.Rules) == 0 {
			continue
		}
		matched := make(map[string]struct{})
		for _, rule := range attrs.Rules {
			if len(rule.RepositoryFilters) == 0 {
				// Empty filter list → whole-registry replication.
				for _, r := range regionRepos {
					matched[r.ID] = struct{}{}
				}
				continue
			}
			for _, f := range rule.RepositoryFilters {
				if sv(f.FilterType) != "PREFIX_MATCH" {
					continue
				}
				prefix := sv(f.Filter)
				if prefix == "" {
					continue
				}
				for _, r := range regionRepos {
					name := repoName(r)
					if name == "" {
						continue
					}
					if strings.HasPrefix(name, prefix) {
						matched[r.ID] = struct{}{}
					}
				}
			}
		}
		for repoID := range matched {
			if err := st.UpsertRelationship(cfg.ID, repoID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ecr replication-config→repo relationship: %w", err)
			}
		}
	}
	return nil
}

// repoName returns the repo name from the row's Name field, falling back to
// the trailing segment of the NativeID ARN (arn:aws:ecr:r:a:repository/<name>).
func repoName(r store.Resource) string {
	if r.Name != nil && *r.Name != "" {
		return *r.Name
	}
	if i := strings.LastIndex(r.NativeID, "/"); i >= 0 {
		return r.NativeID[i+1:]
	}
	return ""
}
