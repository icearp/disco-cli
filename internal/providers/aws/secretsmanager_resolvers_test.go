package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveSecretToKMSKey verifies that a secret with a KmsKeyId pointing at
// a customer-managed key produces a secret→key uses edge.
func TestResolveSecretToKMSKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abcd-1234", testRegion, acct.ID)
	secretARN := fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:prod/db-abc", testRegion, acct.ID)
	attrsJSON := fmt.Sprintf(`{"KmsKeyId": "%s"}`, keyARN)

	keyResID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	secretResID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secretARN, testRegion, attrsJSON)

	if err := resolveSecretsManagerKMS(acct, st); err != nil {
		t.Fatalf("resolveSecretsManagerKMS: %v", err)
	}
	rels, err := st.RelationshipsFrom(secretResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, secretResID, keyResID, store.RelUses)
}

// TestResolveSecretToKMSKey_NoKey verifies secrets using the AWS-managed default
// key (KmsKeyId omitted) produce no edge and no error.
func TestResolveSecretToKMSKey_NoKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	secretARN := fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:prod/nokey", testRegion, acct.ID)
	secretResID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secretARN, testRegion, "{}")

	if err := resolveSecretsManagerKMS(acct, st); err != nil {
		t.Fatalf("resolveSecretsManagerKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(secretResID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}

// TestResolveSecretsManagerRotation verifies that a secret with rotation
// enabled links to its rotation Lambda function.
func TestResolveSecretsManagerRotation(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	secretARN := fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:rot", testRegion, acct.ID)
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:rotator", testRegion, acct.ID)
	attrs := `{"RotationLambdaARN":"` + fnARN + `"}`

	secretID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secretARN, testRegion, attrs)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	if err := resolveSecretsManagerRotation(acct, st); err != nil {
		t.Fatalf("resolveSecretsManagerRotation: %v", err)
	}
	rels, err := st.RelationshipsFrom(secretID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, secretID, fnID, store.RelUses)
}
