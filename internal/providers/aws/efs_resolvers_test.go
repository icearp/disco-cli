package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveEFSFileSystemRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fsARN := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/fs-abc123", testRegion, acct.ID)
	kmsARN := "arn:aws:kms:" + testRegion + ":" + acct.ID + ":key/key-id-1"
	attrs := `{"KmsKeyId":"` + kmsARN + `"}`

	fsID := upsertTestResource(t, st, "aws", acct.ID, TypeEFSFileSystem, fsARN, testRegion, attrs)
	kmsID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kmsARN, testRegion, "{}")

	if err := resolveEFSFileSystemRelationships(acct, st); err != nil {
		t.Fatalf("resolveEFSFileSystemRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, fsID, kmsID, store.RelUses)
}

func TestResolveEFSFileSystemRelationships_NoKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fsARN := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/fs-nokey", testRegion, acct.ID)
	fsID := upsertTestResource(t, st, "aws", acct.ID, TypeEFSFileSystem, fsARN, testRegion, "{}")

	if err := resolveEFSFileSystemRelationships(acct, st); err != nil {
		t.Fatalf("resolveEFSFileSystemRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveEFSMountTargetRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fsID := "fs-abc123"
	mtNativeID := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s/mount-target/fsmt-xyz", testRegion, acct.ID, fsID)
	fsARN := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s", testRegion, acct.ID, fsID)
	subnetID := "subnet-1a2b3c4d"
	attrs := fmt.Sprintf(`{"FileSystemId":%q,"SubnetId":%q}`, fsID, subnetID)

	mtResID := upsertTestResource(t, st, "aws", acct.ID, TypeEFSMountTarget, mtNativeID, testRegion, attrs)
	fsResID := upsertTestResource(t, st, "aws", acct.ID, TypeEFSFileSystem, fsARN, testRegion, "{}")
	subnetResID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", subnetID), testRegion, "{}")

	if err := resolveEFSMountTargetRelationships(acct, st); err != nil {
		t.Fatalf("resolveEFSMountTargetRelationships: %v", err)
	}

	mtRels, err := st.RelationshipsFrom(mtResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom mt: %v", err)
	}
	assertRelationship(t, mtRels, mtResID, subnetResID, store.RelAttachedTo)
	fsRels, err := st.RelationshipsFrom(fsResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom fs: %v", err)
	}
	assertRelationship(t, fsRels, fsResID, mtResID, store.RelContains)
}

func TestResolveEFSAccessPointRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fsID := "fs-abc123"
	apARN := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:access-point/fsap-xyz", testRegion, acct.ID)
	fsARN := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s", testRegion, acct.ID, fsID)
	attrs := fmt.Sprintf(`{"FileSystemId":%q}`, fsID)

	apResID := upsertTestResource(t, st, "aws", acct.ID, TypeEFSAccessPoint, apARN, testRegion, attrs)
	fsResID := upsertTestResource(t, st, "aws", acct.ID, TypeEFSFileSystem, fsARN, testRegion, "{}")

	if err := resolveEFSAccessPointRelationships(acct, st); err != nil {
		t.Fatalf("resolveEFSAccessPointRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fsResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, fsResID, apResID, store.RelContains)
}
