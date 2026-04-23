package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveKinesisStreamRelationships) }

// resolveKinesisStreamRelationships links each stream to its KMS key when
// KMS encryption is enabled with a customer-managed key. alias/aws/kinesis is
// the AWS-managed default and is not scanned.
func resolveKinesisStreamRelationships(acct *account, st *store.Store) error {
	streams, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeKinesisStream},
		Limit: util.AllResources,
	})
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
		if sv(attrs.EncryptionType) != "KMS" || sv(attrs.KeyId) == "" {
			continue
		}
		if strings.HasPrefix(*attrs.KeyId, "alias/aws/") {
			continue
		}
		keyARN := kmsKeyTargetARN(*attrs.KeyId, sv(r.Region), acct.ID)
		keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, keyARN)
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert kinesis-stream→kms: %w", err)
		}
	}
	return nil
}
