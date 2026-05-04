package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveCodeBuildProjectRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/cb-svc", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, fmt.Sprintf(`{"KeyId":"abc-123","Arn":%q}`, keyARN))
	vpcID := "vpc-aaa"
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", vpcID)
	vpcRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	snID := "subnet-bbb"
	snARN := ec2ARN(testRegion, acct.ID, "subnet", snID)
	snRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")
	sgID := "sg-ccc"
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", sgID)
	sgRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	bucketARN := "arn:aws:s3:::artifacts-bucket"
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, "{}")
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/codebuild/proj")
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")

	projARN := fmt.Sprintf("arn:aws:codebuild:%s:%s:project/proj", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{
		"Arn":%q,"Name":"proj",
		"ServiceRole":%q,
		"EncryptionKey":%q,
		"VpcConfig":{"VpcId":%q,"Subnets":[%q],"SecurityGroupIds":[%q]},
		"Artifacts":{"Type":"S3","Location":"artifacts-bucket/path"},
		"LogsConfig":{"CloudWatchLogs":{"Status":"ENABLED","GroupName":"/codebuild/proj"}}
	}`, projARN, roleARN, keyARN, vpcID, snID, sgID)
	projID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeBuildProject, projARN, testRegion, attrs)

	if err := resolveCodeBuildProjectRefs(acct, st); err != nil {
		t.Fatalf("resolveCodeBuildProjectRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(projID)
	assertRelationship(t, rels, projID, roleID, store.RelAssumes)
	assertRelationship(t, rels, projID, keyID, store.RelUses)
	assertRelationship(t, rels, projID, vpcRowID, store.RelAttachedTo)
	assertRelationship(t, rels, projID, snRowID, store.RelAttachedTo)
	assertRelationship(t, rels, projID, sgRowID, store.RelAttachedTo)
	assertRelationship(t, rels, projID, bucketID, store.RelUses)
	assertRelationship(t, rels, projID, lgID, store.RelUses)
}
