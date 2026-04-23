package aws

import (
	"encoding/json"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveSQSQueueRelationships_KMSAndDLQ verifies KMS + DLQ edges for an
// SQS queue with a customer-managed key and a redrive policy.
func TestResolveSQSQueueRelationships_KMSAndDLQ(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	queueARN := "arn:aws:sqs:us-east-1:" + testAccountID + ":main"
	dlqARN := "arn:aws:sqs:us-east-1:" + testAccountID + ":dlq"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/abcd"

	rp, _ := json.Marshal(map[string]any{"deadLetterTargetArn": dlqARN, "maxReceiveCount": 5})
	qAttrs := map[string]string{
		"QueueArn":       queueARN,
		"KmsMasterKeyId": keyARN,
		"RedrivePolicy":  string(rp),
	}
	attrsJSON, _ := json.Marshal(qAttrs)

	qID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, queueARN, testRegion, string(attrsJSON))
	dlqID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, dlqARN, testRegion, "{}")
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveSQSQueueRelationships(acct, st); err != nil {
		t.Fatalf("resolveSQSQueueRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(qID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, qID, keyID, store.RelUses)
	assertRelationship(t, rels, qID, dlqID, store.RelRoutesTo)
}
