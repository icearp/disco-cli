package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
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

// TestResolveECRReplicationConfiguration verifies prefix-match rules link the
// replication-configuration to matching repos, an empty filter list links every
// repo in the region, and other regions are not crossed.
func TestResolveECRReplicationConfiguration(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cfgARN := "arn:aws:ecr:us-east-1:123456789012:replication-configuration"
	cfgAttrs := `{"Rules":[
		{"RepositoryFilters":[{"Filter":"prod-","FilterType":"PREFIX_MATCH"}],"Destinations":[{"Region":"us-west-2","RegistryId":"123456789012"}]},
		{"RepositoryFilters":[],"Destinations":[{"Region":"eu-west-1","RegistryId":"123456789012"}]}
	]}`
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeECRReplicationConfiguration, cfgARN, testRegion, cfgAttrs)

	prodAPI := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, "arn:aws:ecr:us-east-1:123456789012:repository/prod-api", testRegion, "{}")
	prodWeb := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, "arn:aws:ecr:us-east-1:123456789012:repository/prod-web", testRegion, "{}")
	devTools := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, "arn:aws:ecr:us-east-1:123456789012:repository/dev-tools", testRegion, "{}")
	// Different region — must NOT match (replication config is region-scoped).
	otherRegion := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, "arn:aws:ecr:us-west-2:123456789012:repository/prod-other", "us-west-2", "{}")

	if err := resolveECRReplicationConfiguration(acct, st); err != nil {
		t.Fatalf("resolveECRReplicationConfiguration: %v", err)
	}
	rels, err := st.RelationshipsFrom(cfgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	// Empty-filter rule covers all three same-region repos; prefix rule
	// overlaps on prod-* but dedupes. Total: 3 edges, no edge to otherRegion.
	assertRelationship(t, rels, cfgID, prodAPI, store.RelUses)
	assertRelationship(t, rels, cfgID, prodWeb, store.RelUses)
	assertRelationship(t, rels, cfgID, devTools, store.RelUses)
	for _, r := range rels {
		if r.ToID == otherRegion {
			t.Errorf("unexpected cross-region replication edge to %s", otherRegion)
		}
	}
	if len(rels) != 3 {
		t.Errorf("expected 3 edges, got %d", len(rels))
	}
}

// TestResolveECRReplicationConfiguration_NoRules verifies a config with no rules
// (default state) emits no edges.
func TestResolveECRReplicationConfiguration_NoRules(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cfgARN := "arn:aws:ecr:us-east-1:123456789012:replication-configuration"
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeECRReplicationConfiguration, cfgARN, testRegion, `{"Rules":[]}`)
	upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, "arn:aws:ecr:us-east-1:123456789012:repository/app", testRegion, "{}")

	if err := resolveECRReplicationConfiguration(acct, st); err != nil {
		t.Fatalf("resolveECRReplicationConfiguration: %v", err)
	}
	rels, err := st.RelationshipsFrom(cfgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 edges with empty Rules, got %d", len(rels))
	}
}
