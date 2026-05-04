package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveB2BIProfileLogGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pARN := fmt.Sprintf("arn:aws:b2bi:%s:%s:profile/p-1", testRegion, acct.ID)
	lgName := "/aws/b2bi/profile/p-1"
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, lgName)
	attrs := fmt.Sprintf(`{"LogGroupName":%q}`, lgName)

	pID := upsertTestResource(t, st, "aws", acct.ID, TypeB2BIProfile, pARN, testRegion, attrs)
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")

	if err := resolveB2BIProfileLogGroup(acct, st); err != nil {
		t.Fatalf("resolveB2BIProfileLogGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, lID, store.RelUses)
}
