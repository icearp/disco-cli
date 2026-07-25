package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/icearp/disco-cli/store"
)

func backupRecoveryPointAttrs(t *testing.T, rp backuptypes.RecoveryPointByBackupVault) string {
	t.Helper()
	b, err := json.Marshal(rp)
	if err != nil {
		t.Fatalf("backupRecoveryPointAttrs: %v", err)
	}
	return string(b)
}

func TestResolveBackupRecoveryPointRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	vaultARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:Default", testRegion, testAccountID)
	vaultID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupVault, vaultARN, testRegion, "{}")

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/BackupRole", testAccountID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	keyUUID := "abcd1234-aaaa-bbbb-cccc-1234567890ab"
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, testAccountID, keyUUID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion,
		fmt.Sprintf(`{"KeyId":%q,"Arn":%q}`, keyUUID, keyARN))

	rpARN := fmt.Sprintf("arn:aws:backup:%s:%s:recovery-point:abcd-1111", testRegion, testAccountID)
	rpAttrs := backupRecoveryPointAttrs(t, backuptypes.RecoveryPointByBackupVault{
		RecoveryPointArn: &rpARN, BackupVaultArn: &vaultARN, EncryptionKeyArn: &keyARN, IamRoleArn: &roleARN,
	})
	rpID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupRecoveryPoint, rpARN, testRegion, rpAttrs)

	if err := resolveBackupRecoveryPointRefs(acct, st); err != nil {
		t.Fatalf("resolveBackupRecoveryPointRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(rpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, rpID, vaultID, store.RelAttachedTo)
	assertRelationship(t, rels, rpID, keyID, store.RelUses)
	assertRelationship(t, rels, rpID, roleID, store.RelAssumes)
}

// A recovery point with unscanned vault/key/role (cross-account or AWS-managed
// default key) emits no edges and doesn't panic on minimal attrs.
func TestResolveBackupRecoveryPointRefs_Unscanned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rpARN := fmt.Sprintf("arn:aws:backup:%s:%s:recovery-point:orphan-2222", testRegion, testAccountID)
	goneVault := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:Gone", testRegion, testAccountID)
	rpAttrs := backupRecoveryPointAttrs(t, backuptypes.RecoveryPointByBackupVault{
		RecoveryPointArn: &rpARN, BackupVaultArn: &goneVault,
	})
	rpID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupRecoveryPoint, rpARN, testRegion, rpAttrs)

	if err := resolveBackupRecoveryPointRefs(acct, st); err != nil {
		t.Fatalf("resolveBackupRecoveryPointRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(rpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unscanned-target recovery point emitted %d edges, want 0", len(rels))
	}
}
