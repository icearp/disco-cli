package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveBackupRelationships,
		EdgeDecl{TypeBackupVault, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeBackupLogicallyAirGappedVault, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeBackupSelection, TypeIAMRole, store.RelAssumes},
	)
}

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
		Providers: []string{"aws"},
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBackupSelection},
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

func init() {
	registerResolver(
		resolveBackupPlanVaultRefs,
		EdgeDecl{TypeBackupPlan, TypeBackupVault, store.RelRoutesTo},
	)
}

// resolveBackupPlanVaultRefs walks each plan's Rules[] and emits a
// routes-to edge to the TargetBackupVaultName. GetBackupPlan body shape:
// {"BackupPlan":{"Rules":[{"TargetBackupVaultName":"..."}]}}.
func resolveBackupPlanVaultRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeBackupPlan}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vaultSet, err := scannedIDSet(acct, st, TypeBackupVault)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			BackupPlan *struct {
				Rules []struct {
					TargetBackupVaultName *string `json:"TargetBackupVaultName"`
				} `json:"Rules"`
			} `json:"BackupPlan"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.BackupPlan == nil {
			continue
		}
		region := sv(r.Region)
		seen := make(map[string]bool, len(attrs.BackupPlan.Rules))
		for _, rule := range attrs.BackupPlan.Rules {
			n := sv(rule.TargetBackupVaultName)
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			vARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:%s", region, acct.ID, n)
			tgt := store.ResourceID("aws", acct.ID, TypeBackupVault, vARN)
			if !vaultSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert backup-plan→vault: %w", err)
			}
		}
	}
	return nil
}
