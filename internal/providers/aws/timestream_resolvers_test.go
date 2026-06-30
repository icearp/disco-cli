package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveTSInfluxRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cARN := fmt.Sprintf("arn:aws:timestream-influxdb:%s:%s:db-cluster/c1", testRegion, acct.ID)
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	secretARN := fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:influx-auth-AbCd", testRegion, acct.ID)
	bName := "influx-logs"
	bARN := "arn:aws:s3:::" + bName
	attrs := fmt.Sprintf(`{"VpcSubnetIds":["subnet-1"],"VpcSecurityGroupIds":["sg-1"],"InfluxAuthParametersSecretArn":%q,"LogDeliveryConfiguration":{"S3Configuration":{"BucketName":%q}}}`, secretARN, bName)

	cID := upsertTestResource(t, st, "aws", acct.ID, TypeTimestreamInfluxDBCluster, cARN, testRegion, attrs)
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	secID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secretARN, testRegion, "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, testRegion, "{}")

	if err := resolveTSInfluxRefs(acct, st); err != nil {
		t.Fatalf("resolveTSInfluxRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, cID, sgID, store.RelUses)
	assertRelationship(t, rels, cID, secID, store.RelUses)
	assertRelationship(t, rels, cID, bID, store.RelUses)
}

func TestResolveTSDatabaseKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dbARN := fmt.Sprintf("arn:aws:timestream:%s:%s:database/db1", testRegion, acct.ID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"KmsKeyId":%q}`, keyARN)

	dID := upsertTestResource(t, st, "aws", acct.ID, TypeTimestreamDatabase, dbARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveTSDatabaseKMS(acct, st); err != nil {
		t.Fatalf("resolveTSDatabaseKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, kID, store.RelUses)
}

func TestResolveTSTableMagneticS3(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tARN := fmt.Sprintf("arn:aws:timestream:%s:%s:database/db1/table/t1", testRegion, acct.ID)
	bName := "ts-magnetic-rejects"
	bARN := "arn:aws:s3:::" + bName
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"MagneticStoreWriteProperties":{"MagneticStoreRejectedDataLocation":{"S3Configuration":{"BucketName":%q,"KmsKeyId":%q}}}}`, bName, keyARN)

	tID := upsertTestResource(t, st, "aws", acct.ID, TypeTimestreamTable, tARN, testRegion, attrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveTSTableMagneticS3(acct, st); err != nil {
		t.Fatalf("resolveTSTableMagneticS3: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tID)
	assertRelationship(t, rels, tID, bID, store.RelUses)
	assertRelationship(t, rels, tID, kID, store.RelUses)
}

func TestResolveTSScheduledQueryRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	sqARN := fmt.Sprintf("arn:aws:timestream:%s:%s:scheduled-query/sq1", testRegion, acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/ts-sq", acct.ID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	topicARN := fmt.Sprintf("arn:aws:sns:%s:%s:ts-alerts", testRegion, acct.ID)
	bName := "ts-error-reports"
	bARN := "arn:aws:s3:::" + bName
	attrs := fmt.Sprintf(`{"ScheduledQueryExecutionRoleArn":%q,"KmsKeyId":%q,"NotificationConfiguration":{"SnsConfiguration":{"TopicArn":%q}},"ErrorReportConfiguration":{"S3Configuration":{"BucketName":%q}}}`, roleARN, keyARN, topicARN, bName)

	sID := upsertTestResource(t, st, "aws", acct.ID, TypeTimestreamScheduledQuery, sqARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, testRegion, "{}")

	if err := resolveTSScheduledQueryRefs(acct, st); err != nil {
		t.Fatalf("resolveTSScheduledQueryRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, rID, store.RelAssumes)
	assertRelationship(t, rels, sID, kID, store.RelUses)
	assertRelationship(t, rels, sID, tID, store.RelRoutesTo)
	assertRelationship(t, rels, sID, bID, store.RelUses)
}

func TestResolveTSInfluxParameterGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pgID := "pg-0001"
	pgARN := fmt.Sprintf("arn:aws:timestream-influxdb:%s:%s:db-parameter-group/%s", testRegion, acct.ID, pgID)
	instARN := fmt.Sprintf("arn:aws:timestream-influxdb:%s:%s:db-instance/i1", testRegion, acct.ID)

	pgAttrs := fmt.Sprintf(`{"Id":%q}`, pgID)
	instAttrs := fmt.Sprintf(`{"DbParameterGroupIdentifier":%q}`, pgID)

	pgRID := upsertTestResource(t, st, "aws", acct.ID, TypeTimestreamInfluxDBParameterGroup, pgARN, testRegion, pgAttrs)
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeTimestreamInfluxDBInstance, instARN, testRegion, instAttrs)

	if err := resolveTSInfluxParameterGroup(acct, st); err != nil {
		t.Fatalf("resolveTSInfluxParameterGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(instID)
	assertRelationship(t, rels, instID, pgRID, store.RelUses)
}

func TestResolveTSInfluxParameterGroup_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pgARN := fmt.Sprintf("arn:aws:timestream-influxdb:%s:%s:db-parameter-group/pg-x", testRegion, acct.ID)
	upsertTestResource(t, st, "aws", acct.ID, TypeTimestreamInfluxDBParameterGroup, pgARN, testRegion, "{}")
	instARN := fmt.Sprintf("arn:aws:timestream-influxdb:%s:%s:db-instance/i-x", testRegion, acct.ID)
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeTimestreamInfluxDBInstance, instARN, testRegion, "{}")

	if err := resolveTSInfluxParameterGroup(acct, st); err != nil {
		t.Fatalf("resolveTSInfluxParameterGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(instID)
	if len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}
