package aws

import (
	"errors"
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
	rels, err := st.RelationshipsFrom(schemaID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
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
	rels, err := st.RelationshipsFrom(schemaID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected no edge when registry unscanned, got %d", len(rels))
	}
}

// A schema NativeID that names no registry is reported, and the other
// schemas are still linked.
func TestResolveEventSchemasSchemaRegistry_BadNativeIDReportedOthersLinked(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	regARN := fmt.Sprintf("arn:aws:schemas:%s:%s:registry/reg", testRegion, acct.ID)
	regID := upsertTestResource(t, st, "aws", acct.ID, TypeEventSchemasRegistry, regARN, testRegion, "{}")
	for _, bad := range []string{
		fmt.Sprintf("arn:aws:schemas:%s:%s:schema/no-slash", testRegion, acct.ID),
		fmt.Sprintf("arn:aws:schemas:%s:%s:other/reg/x", testRegion, acct.ID),
		fmt.Sprintf("arn:aws:schemas:%s:%s:schema//x", testRegion, acct.ID),
	} {
		upsertTestResource(t, st, "aws", acct.ID, TypeEventSchemasSchema, bad, testRegion, "{}")
	}
	goodID := upsertTestResource(t, st, "aws", acct.ID, TypeEventSchemasSchema,
		fmt.Sprintf("arn:aws:schemas:%s:%s:schema/reg/S", testRegion, acct.ID), testRegion, "{}")

	err := resolveEventSchemasSchemaRegistry(acct, st)
	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) || len(joined.Unwrap()) != 3 {
		t.Fatalf("err = %v, want one error per bad NativeID (3)", err)
	}
	rels, rerr := st.RelationshipsFrom(goodID)
	if rerr != nil {
		t.Fatalf("RelationshipsFrom: %v", rerr)
	}
	assertRelationship(t, rels, goodID, regID, store.RelAttachedTo)
}

// A malformed schema NativeID is reported even when no registry was stored.
func TestResolveEventSchemasSchemaRegistry_BadNativeIDReportedWithoutRegistries(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeEventSchemasSchema,
		fmt.Sprintf("arn:aws:schemas:%s:%s:schema/no-slash", testRegion, acct.ID), testRegion, "{}")
	if err := resolveEventSchemasSchemaRegistry(acct, st); err == nil {
		t.Fatal("err = nil, want the malformed NativeID reported")
	}
}
