package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveACMPCARelationships_CRLBucket(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	caARN := fmt.Sprintf("arn:aws:acm-pca:%s:%s:certificate-authority/abc-123", testRegion, acct.ID)
	bucket := "my-crl-bucket"
	attrs := fmt.Sprintf(`{"RevocationConfiguration":{"CrlConfiguration":{"S3BucketName":%q}}}`, bucket)

	caID := upsertTestResource(t, st, "aws", acct.ID, TypeACMPrivateCA, caARN, testRegion, attrs)
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket, "", "{}")

	if err := resolveACMPCARelationships(acct, st); err != nil {
		t.Fatalf("resolveACMPCARelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(caID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, caID, bucketID, store.RelUses)
}

func TestResolveACMPCARelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	caARN := fmt.Sprintf("arn:aws:acm-pca:%s:%s:certificate-authority/empty", testRegion, acct.ID)
	caID := upsertTestResource(t, st, "aws", acct.ID, TypeACMPrivateCA, caARN, testRegion, "{}")

	if err := resolveACMPCARelationships(acct, st); err != nil {
		t.Fatalf("resolveACMPCARelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(caID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}
