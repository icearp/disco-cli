package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveQBusinessChildrenToApp(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := fmt.Sprintf("arn:aws:qbusiness:%s:%s:application/a1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessApplication, appARN, testRegion, "{}")
	idxARN := appARN + "/index/i1"
	idxID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessIndex, idxARN, testRegion, "{}")
	dsARN := appARN + "/index/i1/data-source/d1"
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessDataSource, dsARN, testRegion, "{}")
	if err := resolveQBusinessChildrenToApp(acct, st); err != nil {
		t.Fatalf("resolveQBusinessChildrenToApp: %v", err)
	}
	rels, _ := st.RelationshipsFrom(idxID)
	assertRelationship(t, rels, idxID, appID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(dsID)
	assertRelationship(t, rels, dsID, appID, store.RelAttachedTo)
}

func TestResolveQBusinessDataSourceToIndex(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	idxARN := fmt.Sprintf("arn:aws:qbusiness:%s:%s:application/a1/index/i1", testRegion, acct.ID)
	idxID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessIndex, idxARN, testRegion, "{}")
	dsARN := idxARN + "/data-source/d1"
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeQBusinessDataSource, dsARN, testRegion, "{}")
	if err := resolveQBusinessDataSourceToIndex(acct, st); err != nil {
		t.Fatalf("resolveQBusinessDataSourceToIndex: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, rels, dsID, idxID, store.RelAttachedTo)
}
