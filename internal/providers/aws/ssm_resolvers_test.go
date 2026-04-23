package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveSSMRelationships_SecureStringToKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyID := "abcd-1234"
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, acct.ID, keyID)
	paramARN := fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/app/db/password", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Type":"SecureString","KeyId":%q}`, keyID)

	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMParameter, paramARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveSSMRelationships(acct, st); err != nil {
		t.Fatalf("resolveSSMRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(pID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, pID, kID, store.RelUses)
}

func TestResolveSSMRelationships_SkipsAWSManagedAlias(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	paramARN := fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/default", testRegion, acct.ID)
	attrs := `{"Type":"SecureString","KeyId":"alias/aws/ssm"}`
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMParameter, paramARN, testRegion, attrs)

	if err := resolveSSMRelationships(acct, st); err != nil {
		t.Fatalf("resolveSSMRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	if len(rels) != 0 {
		t.Errorf("expected 0 rels, got %d", len(rels))
	}
}

func TestResolveSSMRelationships_String_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	paramARN := fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/plain", testRegion, acct.ID)
	attrs := `{"Type":"String"}`
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMParameter, paramARN, testRegion, attrs)

	if err := resolveSSMRelationships(acct, st); err != nil {
		t.Fatalf("resolveSSMRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}
