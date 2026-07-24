package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveEventSchemasSchemaRegistry(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	regName := "discovered-schemas"
	regARN := fmt.Sprintf("arn:aws:schemas:%s:%s:registry/%s", testRegion, acct.ID, regName)
	regID := upsertTestResource(t, st, "aws", acct.ID, TypeEventSchemasRegistry, regARN, testRegion, "{}")

	schemaARN := fmt.Sprintf("arn:aws:schemas:%s:%s:schema/%s/MySchema", testRegion, acct.ID, regName)
	schemaID := upsertTestResource(t, st, "aws", acct.ID, TypeEventSchemasSchema, schemaARN, testRegion, "{}")

	if err := resolveEventSchemasSchemaRegistry(acct, st); err != nil {
		t.Fatalf("resolveEventSchemasSchemaRegistry: %v", err)
	}
	rels, _ := st.RelationshipsFrom(schemaID)
	assertRelationship(t, rels, schemaID, regID, store.RelAttachedTo)
}

func TestResolveEventSchemasSchemaRegistry_NoRegistry(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Schema present but its registry was not scanned — no edge, no error.
	schemaARN := fmt.Sprintf("arn:aws:schemas:%s:%s:schema/orphan-reg/MySchema", testRegion, acct.ID)
	schemaID := upsertTestResource(t, st, "aws", acct.ID, TypeEventSchemasSchema, schemaARN, testRegion, "{}")

	if err := resolveEventSchemasSchemaRegistry(acct, st); err != nil {
		t.Fatalf("resolveEventSchemasSchemaRegistry: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(schemaID); len(rels) != 0 {
		t.Errorf("expected no edge when registry unscanned, got %d", len(rels))
	}
}
