package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveBackupRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	kmsARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/key-1", testRegion, acct.ID)
	vaultARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:Default", testRegion, acct.ID)
	vaultAttrs := fmt.Sprintf(`{"EncryptionKeyArn":%q,"BackupVaultName":"Default"}`, kmsARN)

	planID := "plan-1"
	planARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-plan:%s", testRegion, acct.ID, planID)
	selID := "sel-1"
	selARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-plan:%s/selection/%s", testRegion, acct.ID, planID, selID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/BackupRole", acct.ID)
	selAttrs := fmt.Sprintf(`{"IamRoleArn":%q,"SelectionId":%q}`, roleARN, selID)

	vID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupVault, vaultARN, testRegion, vaultAttrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kmsARN, testRegion, "{}")
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupPlan, planARN, testRegion, "{}")
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupSelection, selARN, testRegion, selAttrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	if err := resolveBackupRelationships(acct, st); err != nil {
		t.Fatalf("resolveBackupRelationships: %v", err)
	}

	vRels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, vRels, vID, kID, store.RelUses)

	sRels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, sRels, sID, pID, store.RelContains)
	assertRelationship(t, sRels, sID, rID, store.RelAssumes)
}

func TestResolveBackupRelationships_SkipsManagedKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	vaultARN := fmt.Sprintf("arn:aws:backup:%s:%s:backup-vault:Default", testRegion, acct.ID)
	attrs := `{"EncryptionKeyArn":"alias/aws/backup"}`
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupVault, vaultARN, testRegion, attrs)

	if err := resolveBackupRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}
