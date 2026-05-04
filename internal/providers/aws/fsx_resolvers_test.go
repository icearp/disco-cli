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

func TestResolveFSxFileSystemRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fsARN2 := fsxARN(testRegion, acct.ID, "file-system", "fs-abc")
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-fsx"
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	subnetARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	eniARN := ec2ARN(testRegion, acct.ID, "network-interface", "eni-1")
	attrs := `{"KmsKeyId":"` + keyARN + `","VpcId":"vpc-1","SubnetIds":["subnet-1"],"NetworkInterfaceIds":["eni-1"]}`

	fsID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxFileSystem, fsARN2, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, testRegion, "{}")
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface, eniARN, testRegion, "{}")

	if err := resolveFSxFileSystemRefs(acct, st); err != nil {
		t.Fatalf("resolveFSxFileSystemRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fsID)
	assertRelationship(t, rels, fsID, kID, store.RelUses)
	assertRelationship(t, rels, fsID, vID, store.RelAttachedTo)
	assertRelationship(t, rels, fsID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, fsID, eID, store.RelAttachedTo)
}
