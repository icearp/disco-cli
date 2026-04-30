package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveBackupRelationships) }

// resolveBackupRelationships emits vault→KMS edges, selection→plan contains,
// and selection→IAM-role assumes. Tag-condition expansion (selection → tagged
// resources) is deferred — the selection's tag criteria would have to be
// matched against the full resources table, a distinct mini-project.
func resolveBackupRelationships(acct *account, st *store.Store) error {
	if err := resolveBackupVaults(acct, st); err != nil {
		return err
	}
	return resolveBackupSelections(acct, st)
}

func resolveBackupVaults(acct *account, st *store.Store) error {
	vaults, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeBackupVault, TypeBackupLogicallyAirGappedVault},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range vaults {
		var attrs struct {
			EncryptionKeyArn *string `json:"EncryptionKeyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		kid, ok := idx.resolveKMSKeyID(sv(attrs.EncryptionKeyArn), sv(r.Region), acct.ID)
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, kid, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert backup-vault→kms: %w", err)
		}
	}
	return nil
}

func resolveBackupSelections(acct *account, st *store.Store) error {
	sels, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeBackupSelection},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range sels {
		// Plan→selection containment is recorded by backup_scanners.go via
		// RecordHierarchyBatch; the unified closure writer emits the
		// matching `contains` row to relationships, so no UpsertRelationship
		// call is needed here. Resolver only handles the IAM role edge.
		var attrs struct {
			IamRoleArn *string `json:"IamRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.IamRoleArn); role != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert backup-selection→iam: %w", err)
			}
		}
	}
	return nil
}
