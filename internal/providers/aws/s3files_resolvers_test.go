package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveS3FilesAll(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketARN := "arn:aws:s3:::data-bucket"
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/s3files-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	snARN := ec2ARN(testRegion, acct.ID, "subnet", "sn-1")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")
	eniARN := ec2ARN(testRegion, acct.ID, "network-interface", "eni-1")
	eniID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface, eniARN, testRegion, "{}")

	fsID := "fs-1"
	fsARN := s3filesFSARN(testRegion, acct.ID, fsID)
	fsAttrs := fmt.Sprintf(`{"FileSystemId":%q,"Bucket":"data-bucket","RoleArn":%q}`, fsID, roleARN)
	fsRowID := upsertTestResource(t, st, "aws", acct.ID, TypeS3FilesFileSystem, fsARN, testRegion, fsAttrs)

	apARN := fsARN + "/access-point/ap-1"
	apAttrs := fmt.Sprintf(`{"FileSystemId":%q}`, fsID)
	apID := upsertTestResource(t, st, "aws", acct.ID, TypeS3FilesAccessPoint, apARN, testRegion, apAttrs)

	mtARN := fsARN + "/mount-target/mt-1"
	mtAttrs := fmt.Sprintf(`{"FileSystemId":%q,"VpcId":"vpc-1","SubnetId":"sn-1","NetworkInterfaceId":"eni-1"}`, fsID)
	mtID := upsertTestResource(t, st, "aws", acct.ID, TypeS3FilesMountTarget, mtARN, testRegion, mtAttrs)

	polARN := fsARN + "/policy"
	polID := upsertTestResource(t, st, "aws", acct.ID, TypeS3FilesFileSystemPolicy, polARN, testRegion, "{}")

	if err := resolveS3FilesFileSystemRefs(acct, st); err != nil {
		t.Fatalf("resolveS3FilesFileSystemRefs: %v", err)
	}
	if err := resolveS3FilesAccessPointRefs(acct, st); err != nil {
		t.Fatalf("resolveS3FilesAccessPointRefs: %v", err)
	}
	if err := resolveS3FilesMountTargetRefs(acct, st); err != nil {
		t.Fatalf("resolveS3FilesMountTargetRefs: %v", err)
	}
	if err := resolveS3FilesPolicyParent(acct, st); err != nil {
		t.Fatalf("resolveS3FilesPolicyParent: %v", err)
	}

	rels, _ := st.RelationshipsFrom(fsRowID)
	assertRelationship(t, rels, fsRowID, bucketID, store.RelUses)
	assertRelationship(t, rels, fsRowID, roleID, store.RelAssumes)
	rels, _ = st.RelationshipsFrom(apID)
	assertRelationship(t, rels, apID, fsRowID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(mtID)
	assertRelationship(t, rels, mtID, fsRowID, store.RelAttachedTo)
	assertRelationship(t, rels, mtID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, mtID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, mtID, eniID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(polID)
	assertRelationship(t, rels, polID, fsRowID, store.RelAttachedTo)
}
