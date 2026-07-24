package aws

import (
	"encoding/json"
	"testing"

	"github.com/icearp/disco-cli/store"
)

// TestResolveSNSTopicRelationships_KMSAndDLQ verifies KMS and SQS DLQ edges.
func TestResolveSNSTopicRelationships_KMSAndDLQ(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	topicARN := "arn:aws:sns:us-east-1:" + testAccountID + ":alerts"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/abcd"
	dlqARN := "arn:aws:sqs:us-east-1:" + testAccountID + ":alerts-dlq"

	rp, _ := json.Marshal(map[string]string{"deadLetterTargetArn": dlqARN})
	topicAttrs := map[string]string{
		"TopicArn":       topicARN,
		"KmsMasterKeyId": keyARN,
		"RedrivePolicy":  string(rp),
	}
	attrsJSON, _ := json.Marshal(topicAttrs)

	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, string(attrsJSON))
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	dlqID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, dlqARN, testRegion, "{}")

	if err := resolveSNSTopicRelationships(acct, st); err != nil {
		t.Fatalf("resolveSNSTopicRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(topicID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, topicID, keyID, store.RelUses)
	assertRelationship(t, rels, topicID, dlqID, store.RelRoutesTo)
}

// TestResolveSNSTopicRelationships_AWSManagedKey verifies the AWS-managed SNS
// default key (alias/aws/sns) is skipped.
func TestResolveSNSTopicRelationships_AWSManagedKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	topicARN := "arn:aws:sns:us-east-1:" + testAccountID + ":bare"
	topicAttrs := map[string]string{"KmsMasterKeyId": "alias/aws/sns"}
	attrsJSON, _ := json.Marshal(topicAttrs)

	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, string(attrsJSON))

	if err := resolveSNSTopicRelationships(acct, st); err != nil {
		t.Fatalf("resolveSNSTopicRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(topicID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels (AWS-managed key skipped), got %d", len(rels))
	}
}

// TestRedrivePolicyDLQ verifies the RedrivePolicy JSON parser.
func TestRedrivePolicyDLQ(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`{"deadLetterTargetArn":"arn:x","maxReceiveCount":5}`, "arn:x"},
		{`{"maxReceiveCount":5}`, ""},
		{`not json`, ""},
		{``, ""},
	}
	for _, tt := range tests {
		if got := redrivePolicyDLQ(tt.in); got != tt.want {
			t.Errorf("redrivePolicyDLQ(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestKMSKeyTargetARN verifies normalization to full ARN for key IDs and aliases.
func TestKMSKeyTargetARN(t *testing.T) {
	region, acct := "us-east-1", "123456789012"
	tests := []struct {
		in, want string
	}{
		{"arn:aws:kms:us-east-1:123456789012:key/abcd", "arn:aws:kms:us-east-1:123456789012:key/abcd"},
		{"abcd-1234", "arn:aws:kms:us-east-1:123456789012:key/abcd-1234"},
		{"alias/my-key", "arn:aws:kms:us-east-1:123456789012:alias/my-key"},
	}
	for _, tt := range tests {
		if got := kmsKeyTargetARN(tt.in, region, acct); got != tt.want {
			t.Errorf("kmsKeyTargetARN(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
