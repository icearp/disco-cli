package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

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
