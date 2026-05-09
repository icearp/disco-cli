package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
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

func TestResolveCodeBuildFleetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	flARN := "arn:aws:codebuild:us-east-1:" + testAccountID + ":fleet/myfleet"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/cb-fleet"
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	snARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	attrs := `{"FleetServiceRole":"` + roleARN + `","VpcConfig":{"VpcId":"vpc-1","Subnets":["subnet-1"],"SecurityGroupIds":["sg-1"]}}`

	fID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeBuildFleet, flARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	if err := resolveCodeBuildFleetRefs(acct, st); err != nil {
		t.Fatalf("resolveCodeBuildFleetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fID)
	assertRelationship(t, rels, fID, rID, store.RelAssumes)
	assertRelationship(t, rels, fID, vID, store.RelAttachedTo)
	assertRelationship(t, rels, fID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, fID, sgID, store.RelAttachedTo)
}

func TestResolveCodeBuildReportGroupRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rgARN := "arn:aws:codebuild:us-east-1:" + testAccountID + ":report-group/rg1"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-rg"
	bucketARN := "arn:aws:s3:::cb-reports"
	attrs := `{"ExportConfig":{"S3Destination":{"Bucket":"cb-reports","EncryptionKey":"` + keyARN + `"}}}`

	rgID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeBuildReportGroup, rgARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, "{}")

	if err := resolveCodeBuildReportGroupRefs(acct, st); err != nil {
		t.Fatalf("resolveCodeBuildReportGroupRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rgID)
	assertRelationship(t, rels, rgID, kID, store.RelUses)
	assertRelationship(t, rels, rgID, bID, store.RelUses)
}
