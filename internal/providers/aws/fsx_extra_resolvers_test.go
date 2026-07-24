package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveFSxBackupRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	fsARN := fmt.Sprintf("arn:aws:fsx:%s:%s:file-system/fs-1", region, acct.ID)
	fsID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxFileSystem, fsARN, region, "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", region, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, region, "{}")
	bkARN := fmt.Sprintf("arn:aws:fsx:%s:%s:backup/backup-1", region, acct.ID)
	attrs := fmt.Sprintf(`{"KmsKeyId":"%s","FileSystem":{"ResourceARN":"%s"}}`, keyARN, fsARN)
	bkID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxBackup, bkARN, region, attrs)
	if err := resolveFSxBackupRelationships(acct, st); err != nil {
		t.Fatalf("resolveFSxBackupRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(bkID)
	assertRelationship(t, rels, bkID, fsID, store.RelAttachedTo)
	assertRelationship(t, rels, bkID, keyID, store.RelUses)
}

func TestResolveFSxBackupRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	bkARN := fmt.Sprintf("arn:aws:fsx:%s:%s:backup/backup-1", region, acct.ID)
	bkID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxBackup, bkARN, region, "{}")
	if err := resolveFSxBackupRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(bkID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveFSxFileCacheRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", region, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, region, "{}")
	fcARN := fmt.Sprintf("arn:aws:fsx:%s:%s:file-cache/fc-1", region, acct.ID)
	attrs := fmt.Sprintf(`{"KmsKeyId":"%s"}`, keyARN)
	fcID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxFileCache, fcARN, region, attrs)
	if err := resolveFSxFileCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveFSxFileCacheRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fcID)
	assertRelationship(t, rels, fcID, keyID, store.RelUses)
}
