package aws

import (
	"context"
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
)

// stubBackup is an in-memory backupAPI used to verify variant vault dispatch.
type stubBackup struct {
	vaults     []backuptypes.BackupVaultListMember
	plans      []backuptypes.BackupPlansListMember
	selections map[string][]backuptypes.BackupSelectionsListMember
}

func (s *stubBackup) ListBackupVaults(_ context.Context, _ *backup.ListBackupVaultsInput, _ ...func(*backup.Options)) (*backup.ListBackupVaultsOutput, error) {
	return &backup.ListBackupVaultsOutput{BackupVaultList: s.vaults}, nil
}

func (s *stubBackup) ListBackupPlans(_ context.Context, _ *backup.ListBackupPlansInput, _ ...func(*backup.Options)) (*backup.ListBackupPlansOutput, error) {
	return &backup.ListBackupPlansOutput{BackupPlansList: s.plans}, nil
}

func (s *stubBackup) ListBackupSelections(_ context.Context, in *backup.ListBackupSelectionsInput, _ ...func(*backup.Options)) (*backup.ListBackupSelectionsOutput, error) {
	id := ""
	if in.BackupPlanId != nil {
		id = *in.BackupPlanId
	}
	return &backup.ListBackupSelectionsOutput{BackupSelectionsList: s.selections[id]}, nil
}

func TestScanBackup_VaultVariantSplit(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stdARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:Default", testRegion, acct.ID)
	airARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:AirGapped", testRegion, acct.ID)
	stdName := "Default"
	airName := "AirGapped"

	stub := &stubBackup{
		vaults: []backuptypes.BackupVaultListMember{
			{BackupVaultArn: &stdARN, BackupVaultName: &stdName},
			{
				BackupVaultArn:  &airARN,
				BackupVaultName: &airName,
				VaultType:       backuptypes.VaultTypeLogicallyAirGappedBackupVault,
			},
		},
	}

	total, _, err := scanBackupAll(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanBackupAll: %v", err)
	}
	if total != 2 {
		t.Fatalf("total=%d want 2", total)
	}

	stdID := store.ResourceID("aws", acct.ID, TypeBackupVault, stdARN)
	airID := store.ResourceID("aws", acct.ID, TypeBackupLogicallyAirGappedVault, airARN)
	if _, err := st.GetResource(stdID); err != nil {
		t.Errorf("standard vault missing: %v", err)
	}
	if _, err := st.GetResource(airID); err != nil {
		t.Errorf("logically-air-gapped vault missing: %v", err)
	}
}
