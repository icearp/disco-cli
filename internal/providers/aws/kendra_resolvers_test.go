package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveKendraChildToIndex(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	idxARN := fmt.Sprintf("arn:aws:kendra:%s:%s:index/i-1", testRegion, acct.ID)
	idxID := upsertTestResource(t, st, "aws", acct.ID, TypeKendraIndex, idxARN, testRegion, "{}")
	dsARN := idxARN + "/data-source/d-1"
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeKendraDataSource, dsARN, testRegion, "{}")
	faqARN := idxARN + "/faq/f-1"
	faqID := upsertTestResource(t, st, "aws", acct.ID, TypeKendraFaq, faqARN, testRegion, "{}")
	if err := resolveKendraChildToIndex(acct, st); err != nil {
		t.Fatalf("resolveKendraChildToIndex: %v", err)
	}
	dsRels, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, dsRels, dsID, idxID, store.RelAttachedTo)
	faqRels, _ := st.RelationshipsFrom(faqID)
	assertRelationship(t, faqRels, faqID, idxID, store.RelAttachedTo)
}
