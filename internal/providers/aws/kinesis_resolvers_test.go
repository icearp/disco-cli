package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveKinesisStreamRelationships_KMS verifies KMS edge for customer-managed keys.
func TestResolveKinesisStreamRelationships_KMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	streamARN := "arn:aws:kinesis:us-east-1:" + testAccountID + ":stream/events"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/abcd"
	attrs := `{"EncryptionType":"KMS","KeyId":"` + keyARN + `"}`

	streamID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, streamARN, testRegion, attrs)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveKinesisStreamRelationships(acct, st); err != nil {
		t.Fatalf("resolveKinesisStreamRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(streamID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, streamID, keyID, store.RelUses)
}

// TestResolveKinesisStreamRelationships_AWSManagedKey verifies alias/aws/kinesis is skipped.
func TestResolveKinesisStreamRelationships_AWSManagedKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	streamARN := "arn:aws:kinesis:us-east-1:" + testAccountID + ":stream/events"
	attrs := `{"EncryptionType":"KMS","KeyId":"alias/aws/kinesis"}`
	streamID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, streamARN, testRegion, attrs)

	if err := resolveKinesisStreamRelationships(acct, st); err != nil {
		t.Fatalf("resolveKinesisStreamRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(streamID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels for AWS-managed key, got %d", len(rels))
	}
}
