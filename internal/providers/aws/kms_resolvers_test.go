package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveKMSAliasToKey verifies an alias with a TargetKeyId resolves to
// an attached-to edge against the key sharing the same region+account.
func TestResolveKMSAliasToKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyID := "abcd-1234"
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, acct.ID, keyID)
	aliasARN := fmt.Sprintf("arn:aws:kms:%s:%s:alias/prod", testRegion, acct.ID)
	attrsJSON := fmt.Sprintf(`{"TargetKeyId": "%s"}`, keyID)

	keyResID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	aliasResID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSAlias, aliasARN, testRegion, attrsJSON)

	if err := resolveKMSAliases(acct, st); err != nil {
		t.Fatalf("resolveKMSAliases: %v", err)
	}
	rels, err := st.RelationshipsFrom(aliasResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, aliasResID, keyResID, store.RelAttachedTo)

	// Inverse: key → alias (contains).
	fromKey, err := st.RelationshipsFrom(keyResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, fromKey, keyResID, aliasResID, store.RelContains)
}

// TestResolveKMSAliasToKey_NoAttrs verifies alias with empty attrs is a no-op.
func TestResolveKMSAliasToKey_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	aliasARN := fmt.Sprintf("arn:aws:kms:%s:%s:alias/orphan", testRegion, acct.ID)
	aliasResID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSAlias, aliasARN, testRegion, "{}")

	if err := resolveKMSAliases(acct, st); err != nil {
		t.Fatalf("resolveKMSAliases: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aliasResID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}
