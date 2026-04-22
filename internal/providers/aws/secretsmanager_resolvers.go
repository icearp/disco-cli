package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveSecretsManagerKMS) }

// resolveSecretsManagerKMS links each secret to the KMS key that encrypts it.
// KmsKeyId is omitted when the AWS-managed default key is used — skip in that
// case since disco doesn't scan AWS-managed keys.
func resolveSecretsManagerKMS(acct *account, st *store.Store) error {
	secrets, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeSecretsManagerSecret},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	keys, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeKMSKey},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	knownKey := make(map[string]bool, len(keys))
	for _, k := range keys {
		knownKey[k.ID] = true
	}
	for _, s := range secrets {
		var attrs struct {
			KmsKeyId *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil || attrs.KmsKeyId == nil || *attrs.KmsKeyId == "" {
			continue
		}
		keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, *attrs.KmsKeyId)
		if !knownKey[keyID] {
			continue
		}
		if err := st.UpsertRelationship(s.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert secret→kms: %w", err)
		}
	}
	return nil
}
