package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveDataSyncLocationS3(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bARN := "arn:aws:s3:::my-bucket"
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/ds-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	agARN := fmt.Sprintf("arn:aws:datasync:%s:%s:agent/agent-1", testRegion, acct.ID)
	agID := upsertTestResource(t, st, "aws", acct.ID, TypeDataSyncAgent, agARN, testRegion, "{}")
	locARN := fmt.Sprintf("arn:aws:datasync:%s:%s:location/loc-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"S3BucketArn":"%s","S3Config":{"BucketAccessRoleArn":"%s"},"AgentArns":["%s"]}`, bARN, roleARN, agARN)
	locID := upsertTestResource(t, st, "aws", acct.ID, TypeDataSyncLocationS3, locARN, testRegion, attrs)
	if err := resolveDataSyncLocationS3(acct, st); err != nil {
		t.Fatalf("resolveDataSyncLocationS3: %v", err)
	}
	rels, _ := st.RelationshipsFrom(locID)
	assertRelationship(t, rels, locID, bID, store.RelUses)
	assertRelationship(t, rels, locID, roleID, store.RelUses)
	assertRelationship(t, rels, locID, agID, store.RelUses)
}

func TestResolveDataSyncLocationEFS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fsARN := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/fs-1", testRegion, acct.ID)
	fsID := upsertTestResource(t, st, "aws", acct.ID, TypeEFSFileSystem, fsARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/efs-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	locARN := fmt.Sprintf("arn:aws:datasync:%s:%s:location/loc-2", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"EfsFilesystemArn":"%s","FileSystemAccessRoleArn":"%s","Ec2Config":{"SubnetArn":"%s","SecurityGroupArns":["%s"]}}`,
		fsARN, roleARN, subARN, sgARN)
	locID := upsertTestResource(t, st, "aws", acct.ID, TypeDataSyncLocationEFS, locARN, testRegion, attrs)
	if err := resolveDataSyncLocationEFS(acct, st); err != nil {
		t.Fatalf("resolveDataSyncLocationEFS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(locID)
	assertRelationship(t, rels, locID, fsID, store.RelAttachedTo)
	assertRelationship(t, rels, locID, roleID, store.RelUses)
	assertRelationship(t, rels, locID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, locID, sgID, store.RelUses)
}

func TestResolveDataSyncLocationFSxOntap(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fsARN := fmt.Sprintf("arn:aws:fsx:%s:%s:file-system/fs-1", testRegion, acct.ID)
	fsID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxFileSystem, fsARN, testRegion, "{}")
	svmARN := fmt.Sprintf("arn:aws:fsx:%s:%s:storage-virtual-machine/fs-1/svm-1", testRegion, acct.ID)
	svmID := upsertTestResource(t, st, "aws", acct.ID, TypeFSxStorageVirtualMachine, svmARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	locARN := fmt.Sprintf("arn:aws:datasync:%s:%s:location/loc-3", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"FsxFilesystemArn":"%s","StorageVirtualMachineArn":"%s","SecurityGroupArns":["%s"]}`, fsARN, svmARN, sgARN)
	locID := upsertTestResource(t, st, "aws", acct.ID, TypeDataSyncLocationFSxONTAP, locARN, testRegion, attrs)
	if err := resolveDataSyncLocationFSxOntap(acct, st); err != nil {
		t.Fatalf("resolveDataSyncLocationFSxOntap: %v", err)
	}
	rels, _ := st.RelationshipsFrom(locID)
	assertRelationship(t, rels, locID, fsID, store.RelAttachedTo)
	assertRelationship(t, rels, locID, svmID, store.RelAttachedTo)
	assertRelationship(t, rels, locID, sgID, store.RelUses)
}

func TestResolveDataSyncOnPremAgents(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	agARN := fmt.Sprintf("arn:aws:datasync:%s:%s:agent/agent-1", testRegion, acct.ID)
	agID := upsertTestResource(t, st, "aws", acct.ID, TypeDataSyncAgent, agARN, testRegion, "{}")
	locARN := fmt.Sprintf("arn:aws:datasync:%s:%s:location/loc-4", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"OnPremConfig":{"AgentArns":["%s"]}}`, agARN)
	locID := upsertTestResource(t, st, "aws", acct.ID, TypeDataSyncLocationNFS, locARN, testRegion, attrs)
	if err := resolveDataSyncOnPremAgents(acct, st); err != nil {
		t.Fatalf("resolveDataSyncOnPremAgents: %v", err)
	}
	rels, _ := st.RelationshipsFrom(locID)
	assertRelationship(t, rels, locID, agID, store.RelUses)
}

func TestResolveDataSyncAgentRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	agARN := "arn:aws:datasync:us-east-1:" + testAccountID + ":agent/agent-1"
	vpceARN := ec2ARN(testRegion, acct.ID, "vpc-endpoint", "vpce-1")
	snARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	attrs := `{"PrivateLinkConfig":{"VpcEndpointId":"vpce-1","SubnetArns":["` + snARN + `"],"SecurityGroupArns":["` + sgARN + `"]}}`

	aID := upsertTestResource(t, st, "aws", acct.ID, TypeDataSyncAgent, agARN, testRegion, attrs)
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpoint, vpceARN, testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	if err := resolveDataSyncAgentRefs(acct, st); err != nil {
		t.Fatalf("resolveDataSyncAgentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, vID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, sgID, store.RelAttachedTo)
}

func TestResolveDataSyncTaskRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tARN := "arn:aws:datasync:us-east-1:" + testAccountID + ":task/task-1"
	lgARN := "arn:aws:logs:us-east-1:" + testAccountID + ":log-group:/datasync/lg1"
	attrs := `{"CloudWatchLogGroupArn":"` + lgARN + `:*"}`

	tID := upsertTestResource(t, st, "aws", acct.ID, TypeDataSyncTask, tARN, testRegion, attrs)
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")

	if err := resolveDataSyncTaskRefs(acct, st); err != nil {
		t.Fatalf("resolveDataSyncTaskRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tID)
	assertRelationship(t, rels, tID, lID, store.RelUses)
}
