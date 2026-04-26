package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveKinesisStreamRelationships) }

// resolveKinesisStreamRelationships links each stream to its KMS key when
// KMS encryption is enabled.
func resolveKinesisStreamRelationships(acct *account, st *store.Store) error {
	streams, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeKinesisStream},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range streams {
		var attrs struct {
			EncryptionType *string `json:"EncryptionType"`
			KeyId          *string `json:"KeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if sv(attrs.EncryptionType) != "KMS" {
			continue
		}
		keyID, ok := kmsIdx.resolveKMSKeyID(sv(attrs.KeyId), sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert kinesis-stream→kms: %w", err)
		}
	}
	return nil
}
