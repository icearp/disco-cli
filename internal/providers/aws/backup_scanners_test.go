package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
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

func (s *stubBackup) GetBackupPlan(_ context.Context, _ *backup.GetBackupPlanInput, _ ...func(*backup.Options)) (*backup.GetBackupPlanOutput, error) {
	return &backup.GetBackupPlanOutput{}, nil
}

func (s *stubBackup) ListBackupSelections(_ context.Context, in *backup.ListBackupSelectionsInput, _ ...func(*backup.Options)) (*backup.ListBackupSelectionsOutput, error) {
	id := ""
	if in.BackupPlanId != nil {
		id = *in.BackupPlanId
	}
	return &backup.ListBackupSelectionsOutput{BackupSelectionsList: s.selections[id]}, nil
}

func (s *stubBackup) ListFrameworks(_ context.Context, _ *backup.ListFrameworksInput, _ ...func(*backup.Options)) (*backup.ListFrameworksOutput, error) {
	return &backup.ListFrameworksOutput{}, nil
}

func (s *stubBackup) ListReportPlans(_ context.Context, _ *backup.ListReportPlansInput, _ ...func(*backup.Options)) (*backup.ListReportPlansOutput, error) {
	return &backup.ListReportPlansOutput{}, nil
}

func (s *stubBackup) ListRestoreTestingPlans(_ context.Context, _ *backup.ListRestoreTestingPlansInput, _ ...func(*backup.Options)) (*backup.ListRestoreTestingPlansOutput, error) {
	return &backup.ListRestoreTestingPlansOutput{}, nil
}

func (s *stubBackup) ListRestoreTestingSelections(_ context.Context, _ *backup.ListRestoreTestingSelectionsInput, _ ...func(*backup.Options)) (*backup.ListRestoreTestingSelectionsOutput, error) {
	return &backup.ListRestoreTestingSelectionsOutput{}, nil
}

func (s *stubBackup) ListTieringConfigurations(_ context.Context, _ *backup.ListTieringConfigurationsInput, _ ...func(*backup.Options)) (*backup.ListTieringConfigurationsOutput, error) {
	return &backup.ListTieringConfigurationsOutput{}, nil
}

func (s *stubBackup) ListLegalHolds(_ context.Context, _ *backup.ListLegalHoldsInput, _ ...func(*backup.Options)) (*backup.ListLegalHoldsOutput, error) {
	return &backup.ListLegalHoldsOutput{}, nil
}

func (s *stubBackup) ListRecoveryPointsByBackupVault(_ context.Context, _ *backup.ListRecoveryPointsByBackupVaultInput, _ ...func(*backup.Options)) (*backup.ListRecoveryPointsByBackupVaultOutput, error) {
	return &backup.ListRecoveryPointsByBackupVaultOutput{}, nil
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

	stdID := store.ResourceID("aws", acct.ID, stdARN)
	airID := store.ResourceID("aws", acct.ID, airARN)
	if _, err := st.GetResource(stdID); err != nil {
		t.Errorf("standard vault missing: %v", err)
	}
	if _, err := st.GetResource(airID); err != nil {
		t.Errorf("logically-air-gapped vault missing: %v", err)
	}
}
