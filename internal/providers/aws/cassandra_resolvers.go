package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveCassandraTableKMS,
		EdgeDecl{TypeCassandraTable, TypeKMSKey, store.RelUses},
	)
}

// resolveCassandraTableKMS wires each Keyspaces table to its CMK
// (EncryptionSpecification.KmsKeyIdentifier — present only on the GetTable
// body that the scanner now fans out per row). Tables using AWS_OWNED_KMS_KEY
// have no KmsKeyIdentifier field and emit no edge.
func resolveCassandraTableKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCassandraTable}, Limit: util.AllResources,
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
			EncryptionSpecification *struct {
				KmsKeyIdentifier *string `json:"KmsKeyIdentifier"`
			} `json:"EncryptionSpecification"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EncryptionSpecification == nil {
			continue
		}
		k := sv(attrs.EncryptionSpecification.KmsKeyIdentifier)
		if k == "" {
			continue
		}
		if keyID, ok := idx.resolveKMSKeyID(k, sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cassandra table→kms: %w", err)
			}
		}
	}
	return nil
}
