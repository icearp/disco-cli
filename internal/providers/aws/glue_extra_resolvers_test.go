package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveGlueUserDefinedFunctionRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	dbARN := glueDatabaseARN(region, acct.ID, "mydb")
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueDatabase, dbARN, region, "{}")
	udfID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueUserDefinedFunction, dbARN+"/function/fn1", region, `{"FunctionName":"fn1"}`)
	if err := resolveGlueUserDefinedFunctionRelationships(acct, st); err != nil {
		t.Fatalf("resolveGlueUserDefinedFunctionRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(udfID)
	assertRelationship(t, rels, udfID, dbID, store.RelAttachedTo)
}

func TestResolveGlueUserDefinedFunctionRelationships_NoParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	dbARN := glueDatabaseARN(region, acct.ID, "absent")
	udfID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueUserDefinedFunction, dbARN+"/function/fn1", region, "{}")
	if err := resolveGlueUserDefinedFunctionRelationships(acct, st); err != nil {
		t.Fatalf("no-parent: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(udfID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
