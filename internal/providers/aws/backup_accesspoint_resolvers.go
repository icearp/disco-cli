package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveBackupAccessPointRefs,
		EdgeDecl{TypeBackupAccessPoint, TypeBackupRecoveryPoint, store.RelAttachedTo},
		EdgeDecl{TypeBackupAccessPoint, TypeBackupVault, store.RelAttachedTo},
		EdgeDecl{TypeBackupAccessPoint, TypeBackupLogicallyAirGappedVault, store.RelAttachedTo},
	)
}

// resolveBackupAccessPointRefs wires each access point to the recovery point it
// exposes and to that recovery point's vault. ResourceArn (the backed-up
// resource) is left unwired for the reason resolveBackupRecoveryPointRefs gives.
// A row whose attributes do not parse is reported in the returned error after
// the remaining rows are wired, so one bad row does not cost every later edge.
func resolveBackupAccessPointRefs(acct *account, st *store.Store) error {
	aps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeBackupAccessPoint}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(aps) == 0 {
		return nil
	}
	targetByARN, err := nativeIDToResourceID(st, acct.ID, TypeBackupRecoveryPoint, TypeBackupVault, TypeBackupLogicallyAirGappedVault)
	if err != nil {
		return err
	}
	var bad []error
	for _, ap := range aps {
		var attrs struct {
			RecoveryPointArn *string `json:"RecoveryPointArn"`
			BackupVaultArn   *string `json:"BackupVaultArn"`
		}
		if err := json.Unmarshal([]byte(ap.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("backup access point %s attributes: %w", ap.NativeID, err))
			continue
		}
		for _, ref := range []string{sv(attrs.RecoveryPointArn), sv(attrs.BackupVaultArn)} {
			tgt, ok := targetByARN[ref]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(ap.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert backup-access-point→%s: %w", ref, err)
			}
		}
	}
	return errors.Join(bad...)
}
