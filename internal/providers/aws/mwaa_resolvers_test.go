package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveMWAAEnvironmentRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	envARN := "arn:aws:airflow:us-east-1:" + testAccountID + ":environment/Prod"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/mwaa-exec"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-mw"
	bucketARN := "arn:aws:s3:::dags-prod"
	subnetARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	attrs := `{"ExecutionRoleArn":"` + roleARN + `","KmsKey":"` + keyARN +
		`","SourceBucketArn":"` + bucketARN +
		`","NetworkConfiguration":{"SubnetIds":["subnet-1"],"SecurityGroupIds":["sg-1"]}}`

	eID := upsertTestResource(t, st, "aws", acct.ID, TypeMWAAEnvironment, envARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	if err := resolveMWAAEnvironmentRefs(acct, st); err != nil {
		t.Fatalf("resolveMWAAEnvironmentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eID)
	assertRelationship(t, rels, eID, rID, store.RelAssumes)
	assertRelationship(t, rels, eID, kID, store.RelUses)
	assertRelationship(t, rels, eID, bID, store.RelUses)
	assertRelationship(t, rels, eID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, eID, sgID, store.RelAttachedTo)
}
