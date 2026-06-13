package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDSQLClusterKMS,
		EdgeDecl{TypeDSQLCluster, TypeKMSKey, store.RelUses},
	)
}

// resolveDSQLClusterKMS wires each Aurora DSQL cluster to its CMEK via
// EncryptionDetails.KmsKeyArn. FK-safe via the shared KMS index.
func resolveDSQLClusterKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDSQLCluster}, Limit: util.AllResources,
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
			EncryptionDetails *struct {
				KmsKeyArn *string `json:"KmsKeyArn"`
			} `json:"EncryptionDetails"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EncryptionDetails == nil {
			continue
		}
		ref := sv(attrs.EncryptionDetails.KmsKeyArn)
		if ref == "" {
			continue
		}
		tgt, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert dsql cluster→kms: %w", err)
		}
	}
	return nil
}
