package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveSSMRelationships) }

// resolveSSMRelationships emits edges for SecureString parameters → KMS keys.
// Alias-name references are normalized to the underlying key via the KMS index
// so the edge always points at the canonical key resource.
func resolveSSMRelationships(acct *account, st *store.Store) error {
	params, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMParameter},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	type attrs struct {
		KeyId *string `json:"KeyId"`
		Type  string  `json:"Type"`
	}
	for _, r := range params {
		var a attrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if a.Type != "SecureString" {
			continue
		}
		region := ""
		if r.Region != nil {
			region = *r.Region
		}
		keyID, ok := kmsIdx.resolveKMSKeyID(sv(a.KeyId), region, acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ssm-parameter→kms: %w", err)
		}
	}
	return nil
}
