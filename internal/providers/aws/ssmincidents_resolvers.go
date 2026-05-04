package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveSSMIRSetKMS,
		EdgeDecl{TypeSSMIncidentsReplicationSet, TypeKMSKey, store.RelUses},
	)
}

// resolveSSMIRSetKMS wires replication-set → per-region KMS keys via
// RegionMap[region].SseKmsKeyId.
func resolveSSMIRSetKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMIncidentsReplicationSet}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RegionMap map[string]struct {
				SseKmsKeyId *string `json:"SseKmsKeyId"`
			} `json:"RegionMap"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for region, info := range attrs.RegionMap {
			ref := sv(info.SseKmsKeyId)
			if ref == "" {
				continue
			}
			if keyID, ok := kmsIdx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ssmi rset→kms: %w", err)
				}
			}
		}
	}
	return nil
}
