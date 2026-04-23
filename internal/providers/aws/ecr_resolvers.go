package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveECRRepositoryRelationships) }

// resolveECRRepositoryRelationships links each ECR repository to the KMS key
// that encrypts its image layers when encryption type is KMS. AES256
// encryption uses AWS-owned keys that disco doesn't scan.
func resolveECRRepositoryRelationships(acct *account, st *store.Store) error {
	repos, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
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
