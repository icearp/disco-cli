package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveOpenSearchDataSourceRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	domainARN := fmt.Sprintf("arn:aws:es:%s:%s:domain/my-dom", testRegion, testAccountID)
	domID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDomain, domainARN, testRegion, "{}")
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDataSource, domainARN+"/data-source/ds-1", testRegion, "{}")
	if err := resolveOpenSearchDataSourceRelationships(acct, st); err != nil {
		t.Fatalf("resolveOpenSearchDataSourceRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, rels, dsID, domID, store.RelAttachedTo)
}

func TestResolveOpenSearchDataSourceRelationships_NoParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	domainARN := fmt.Sprintf("arn:aws:es:%s:%s:domain/absent", testRegion, testAccountID)
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDataSource, domainARN+"/data-source/ds-1", testRegion, "{}")
	if err := resolveOpenSearchDataSourceRelationships(acct, st); err != nil {
		t.Fatalf("no-parent: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(dsID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
