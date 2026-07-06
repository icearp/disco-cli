package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveSecretToKMSKey verifies a secret with a customer-managed KmsKeyId
// produces a secret→key uses edge.
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

// TestResolveSecretToKMSKey_NoKey verifies the AWS-managed default key
// (KmsKeyId omitted) produces no edge and no error.
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

// TestResolveSecretsManagerRotation verifies a secret with rotation enabled
// links to its rotation Lambda.
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

func TestResolveSecretsManagerReplication(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	replicaRegion := "us-west-2"
	primaryRegion := "us-east-1"
	secretARN := fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:my-secret-aBcDe1", replicaRegion, acct.ID)
	primaryARN := fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:my-secret-aBcDe1", primaryRegion, acct.ID)

	attrs := fmt.Sprintf(`{"PrimaryRegion":%q}`, primaryRegion)
	replicaID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secretARN, replicaRegion, attrs)
	primaryID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, primaryARN, primaryRegion, "{}")

	if err := resolveSecretsManagerReplication(acct, st); err != nil {
		t.Fatalf("resolveSecretsManagerReplication: %v", err)
	}
	rels, err := st.RelationshipsFrom(replicaID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, replicaID, primaryID, store.RelAttachedTo)
}
