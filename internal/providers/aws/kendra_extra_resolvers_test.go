package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveKendraExtraChildToIndex(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	idxARN := fmt.Sprintf("arn:aws:kendra:%s:%s:index/i-1", testRegion, acct.ID)
	idxID := upsertTestResource(t, st, "aws", acct.ID, TypeKendraIndex, idxARN, testRegion, "{}")

	childIDs := make(map[string]string, len(kendraExtraChildren))
	for _, c := range kendraExtraChildren {
		childARN := idxARN + c.seg + "x-1"
		childIDs[c.t] = upsertTestResource(t, st, "aws", acct.ID, c.t, childARN, testRegion, "{}")
	}

	if err := resolveKendraExtraChildToIndex(acct, st); err != nil {
		t.Fatalf("resolveKendraExtraChildToIndex: %v", err)
	}
	for _, cid := range childIDs {
		rels, _ := st.RelationshipsFrom(cid)
		assertRelationship(t, rels, cid, idxID, store.RelAttachedTo)
	}
}

func TestResolveKendraExtraChildToIndex_NoIndex(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	// Child rows exist but the parent index was never scanned — no edge, no FK error.
	idxARN := fmt.Sprintf("arn:aws:kendra:%s:%s:index/i-missing", testRegion, acct.ID)
	thARN := idxARN + "/thesaurus/t-1"
	thID := upsertTestResource(t, st, "aws", acct.ID, TypeKendraThesaurus, thARN, testRegion, "{}")

	if err := resolveKendraExtraChildToIndex(acct, st); err != nil {
		t.Fatalf("resolveKendraExtraChildToIndex: %v", err)
	}
	rels, _ := st.RelationshipsFrom(thID)
	if len(rels) != 0 {
		t.Errorf("expected no edges when index absent, got %d", len(rels))
	}
}
