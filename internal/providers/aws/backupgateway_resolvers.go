package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveBackupGatewayRelationships,
		EdgeDecl{TypeBackupGatewayHypervisor, TypeKMSKey, store.RelUses},
	)
}

// resolveBackupGatewayRelationships emits hypervisor→KMS edges via the
// shared KMS resolver index. KmsKeyArn is empty for hypervisors that use
// the default AWS-managed key — index resolution skips dangling targets.
func resolveBackupGatewayRelationships(acct *account, st *store.Store) error {
	hyps, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeBackupGatewayHypervisor},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range hyps {
		var attrs struct {
			KmsKeyArn *string `json:"KmsKeyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		kid, ok := idx.resolveKMSKeyID(sv(attrs.KmsKeyArn), sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, kid, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert backupgateway-hypervisor→kms: %w", err)
		}
	}
	return nil
}
