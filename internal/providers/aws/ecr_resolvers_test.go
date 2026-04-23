package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveECRRepositoryRelationships_KMS verifies KMS link for repositories
// with customer-managed KMS encryption.
func TestResolveECRRepositoryRelationships_KMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	repoARN := "arn:aws:ecr:us-east-1:123456789012:repository/app"
	keyARN := "arn:aws:kms:us-east-1:123456789012:key/abcd-1234"
	attrs := `{"EncryptionConfiguration":{"EncryptionType":"KMS","KmsKey":"` + keyARN + `"}}`

	repoID := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, repoARN, testRegion, attrs)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveECRRepositoryRelationships(acct, st); err != nil {
		t.Fatalf("resolveECRRepositoryRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(repoID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, repoID, keyID, store.RelUses)
}

// TestResolveECRRepositoryRelationships_AES256 verifies that AES256-encrypted
// repositories (no KmsKey) produce no relationships.
func TestResolveECRRepositoryRelationships_AES256(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	repoARN := "arn:aws:ecr:us-east-1:123456789012:repository/aes"
	attrs := `{"EncryptionConfiguration":{"EncryptionType":"AES256"}}`

	repoID := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, repoARN, testRegion, attrs)

	if err := resolveECRRepositoryRelationships(acct, st); err != nil {
		t.Fatalf("resolveECRRepositoryRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(repoID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveECRRepositoryRelationships_NoAttrs verifies empty attrs is a no-op.
func TestResolveECRRepositoryRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	repoARN := "arn:aws:ecr:us-east-1:123456789012:repository/bare"
	upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, repoARN, testRegion, "{}")

	if err := resolveECRRepositoryRelationships(acct, st); err != nil {
		t.Fatalf("resolveECRRepositoryRelationships: %v", err)
	}
}
