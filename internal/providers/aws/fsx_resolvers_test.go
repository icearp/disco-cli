package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveFSxChildrenToFileSystem(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fsARN := fsxARN(testRegion, acct.ID, "file-system", "fs-1")
	fsID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxFileSystem, fsARN, testRegion, "{}")
	vARN := fsxARN(testRegion, acct.ID, "volume", "fsvol-1")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxVolume, vARN, testRegion, `{"FileSystemId":"fs-1"}`)
	svmARN := fsxARN(testRegion, acct.ID, "storage-virtual-machine", "svm-1")
	svmID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxStorageVirtualMachine, svmARN, testRegion, `{"FileSystemId":"fs-1"}`)
	draARN := fsxARN(testRegion, acct.ID, "association", "dra-1")
	draID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxDataRepositoryAssociation, draARN, testRegion, `{"FileSystemId":"fs-1"}`)

	if err := resolveFSxChildrenToFileSystem(acct, st); err != nil {
		t.Fatalf("resolveFSxChildrenToFileSystem: %v", err)
	}
	for _, c := range []string{vID, svmID, draID} {
		rels, _ := st.RelationshipsFrom(c)
		assertRelationship(t, rels, c, fsID, store.RelAttachedTo)
	}
}

func TestResolveFSxSnapshotToVolume(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vARN := fsxARN(testRegion, acct.ID, "volume", "fsvol-1")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxVolume, vARN, testRegion, "{}")
	snARN := fsxARN(testRegion, acct.ID, "snapshot", "fsvolsnap-1")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxSnapshot, snARN, testRegion, `{"VolumeId":"fsvol-1"}`)
	if err := resolveFSxSnapshotToVolume(acct, st); err != nil {
		t.Fatalf("resolveFSxSnapshotToVolume: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snID)
	assertRelationship(t, rels, snID, vID, store.RelAttachedTo)
}
