package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveCloudTrailRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	trailARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:trail/my-trail", testRegion, acct.ID)
	bucket := "my-audit-bucket"
	kmsARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/key-abc", testRegion, acct.ID)
	lgARN := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:/aws/cloudtrail/my-trail:*", testRegion, acct.ID)

	attrs := fmt.Sprintf(
		`{"Trail":{"S3BucketName":%q,"KMSKeyId":%q,"CloudWatchLogsLogGroupArn":%q},"Status":null}`,
		bucket, kmsARN, lgARN,
	)

	trailID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailTrail, trailARN, testRegion, attrs)
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket, "", "{}")
	kmsID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kmsARN, testRegion, "{}")
	// Log group NativeID has trailing ":*" stripped.
	lgNativeID := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:/aws/cloudtrail/my-trail", testRegion, acct.ID)
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgNativeID, testRegion, "{}")

	if err := resolveCloudTrailRelationships(acct, st); err != nil {
		t.Fatalf("resolveCloudTrailRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(trailID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, trailID, bucketID, store.RelUses)
	assertRelationship(t, rels, trailID, kmsID, store.RelUses)
	assertRelationship(t, rels, trailID, lgID, store.RelUses)
}

func TestResolveCloudTrailRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	trailARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:trail/bare-trail", testRegion, acct.ID)
	trailID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailTrail, trailARN, testRegion, `{"Trail":{},"Status":null}`)

	if err := resolveCloudTrailRelationships(acct, st); err != nil {
		t.Fatalf("resolveCloudTrailRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(trailID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
