package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveCodeCommitRepoKMS,
		EdgeDecl{TypeCodeCommitRepository, TypeKMSKey, store.RelUses},
	)
}

// resolveCodeCommitRepoKMS wires each repository to its CMEK
// (RepositoryMetadata.KmsKeyId; defaults to AWS-managed key when empty).
func resolveCodeCommitRepoKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCodeCommitRepository}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyID *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ref := sv(attrs.KmsKeyID)
		if ref == "" {
			continue
		}
		if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert codecommit→kms: %w", err)
			}
		}
	}
	return nil
}
