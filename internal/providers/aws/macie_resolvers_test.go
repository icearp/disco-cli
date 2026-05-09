package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveMacieClassificationJobBuckets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucket := "macie-target-bucket"
	jobARN := fmt.Sprintf("arn:aws:macie2:%s:%s:classification-job/abc123", testRegion, acct.ID)
	jobAttrs := fmt.Sprintf(`{"JobArn":%q,"S3JobDefinition":{"BucketDefinitions":[{"AccountId":%q,"Buckets":[%q,"unscanned-bucket"]}]}}`, jobARN, acct.ID, bucket)

	jID := upsertTestResource(t, st, "aws", acct.ID, TypeMacieClassificationJob, jobARN, testRegion, jobAttrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket, "", "{}")

	if err := resolveMacieClassificationJobBuckets(acct, st); err != nil {
		t.Fatalf("resolveMacieClassificationJobBuckets: %v", err)
	}

	rels, _ := st.RelationshipsFrom(jID)
	assertRelationship(t, rels, jID, bID, store.RelUses)
	if len(rels) != 1 {
		t.Errorf("got %d edges, want 1 (unscanned-bucket should skip)", len(rels))
	}
}

func TestResolveMacieClassificationJobBuckets_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	jobARN := fmt.Sprintf("arn:aws:macie2:%s:%s:classification-job/empty", testRegion, acct.ID)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeMacieClassificationJob, jobARN, testRegion, "{}")

	if err := resolveMacieClassificationJobBuckets(acct, st); err != nil {
		t.Fatalf("resolveMacieClassificationJobBuckets: %v", err)
	}
}

func TestResolveMacieAllowListBucket(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucket := "macie-allow-list-bucket"
	listARN := fmt.Sprintf("arn:aws:macie2:%s:%s:allow-list/al-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Arn":%q,"Criteria":{"S3WordsList":{"BucketName":%q,"ObjectKey":"words.txt"}}}`, listARN, bucket)

	lID := upsertTestResource(t, st, "aws", acct.ID, TypeMacieAllowList, listARN, testRegion, attrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket, "", "{}")

	if err := resolveMacieAllowListBucket(acct, st); err != nil {
		t.Fatalf("resolveMacieAllowListBucket: %v", err)
	}

	rels, _ := st.RelationshipsFrom(lID)
	assertRelationship(t, rels, lID, bID, store.RelUses)
}

func TestResolveMacieAllowListBucket_RegexOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	listARN := fmt.Sprintf("arn:aws:macie2:%s:%s:allow-list/al-2", testRegion, acct.ID)
	attrs := `{"Criteria":{"Regex":"^TEST-[0-9]+$"}}`

	lID := upsertTestResource(t, st, "aws", acct.ID, TypeMacieAllowList, listARN, testRegion, attrs)

	if err := resolveMacieAllowListBucket(acct, st); err != nil {
		t.Fatalf("resolveMacieAllowListBucket: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lID)
	if len(rels) != 0 {
		t.Errorf("regex-only allow list should emit no edges, got %d", len(rels))
	}
}
