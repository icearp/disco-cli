package aws

import (
	"encoding/json"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveBackupRecoveryPointRefs,
		EdgeDecl{TypeBackupRecoveryPoint, TypeBackupVault, store.RelAttachedTo},
		EdgeDecl{TypeBackupRecoveryPoint, TypeBackupLogicallyAirGappedVault, store.RelAttachedTo},
		EdgeDecl{TypeBackupRecoveryPoint, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeBackupRecoveryPoint, TypeIAMRole, store.RelAssumes},
	)
}

// resolveBackupRecoveryPointRefs wires each recovery point to its vault
// (BackupVaultArn), its encryption KMS key, and the IAM role that created it.
// ResourceArn (the source resource) is left unwired: it spans every backable
// service and disco has no cross-type NativeID index, so an edge would
// FK-drop more often than not. All edges are FK-safe.
func resolveBackupRecoveryPointRefs(acct *account, st *store.Store) error {
	rps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeBackupRecoveryPoint}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rps) == 0 {
		return nil
	}
	vaultByARN, err := nativeIDToResourceID(st, acct.ID, TypeBackupVault, TypeBackupLogicallyAirGappedVault)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	kms, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, rp := range rps {
		var attrs struct {
			BackupVaultArn   *string `json:"BackupVaultArn"`
			EncryptionKeyArn *string `json:"EncryptionKeyArn"`
			IamRoleArn       *string `json:"IamRoleArn"`
		}
		if err := json.Unmarshal([]byte(rp.AttributesJSON), &attrs); err != nil {
			continue
		}
		if v := sv(attrs.BackupVaultArn); v != "" {
			if tgt, ok := vaultByARN[v]; ok {
				if err := st.UpsertRelationship(rp.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return err
				}
			}
		}
		if ref := sv(attrs.EncryptionKeyArn); ref != "" {
			if keyID, ok := kms.resolveKMSKeyID(ref, sv(rp.Region), acct.ID); ok {
				if err := st.UpsertRelationship(rp.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return err
				}
			}
		}
		if role := sv(attrs.IamRoleArn); role != "" {
			tgt := store.ResourceID("aws", acct.ID, role)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(rp.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// nativeIDToResourceID maps each scanned row's NativeID → its resource id across
// the given types (for FK-safe ARN-target lookups where the target spans types).
func nativeIDToResourceID(st *store.Store, accountID string, types ...string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: accountID, Types: types, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		if r.NativeID != "" {
			idx[r.NativeID] = r.ID
		}
	}
	return idx, nil
}
