package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveEntityResolutionPolicyStatementToParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	mwARN := fmt.Sprintf("arn:aws:entityresolution:%s:%s:matchingworkflow/mw-1", testRegion, acct.ID)
	mwID := upsertTestResource(t, st, "aws", acct.ID, TypeEntityResolutionMatchingWorkflow, mwARN, testRegion, "{}")
	imwARN := fmt.Sprintf("arn:aws:entityresolution:%s:%s:idmappingworkflow/im-1", testRegion, acct.ID)
	imwID := upsertTestResource(t, st, "aws", acct.ID, TypeEntityResolutionIdMappingWorkflow, imwARN, testRegion, "{}")

	psMW := upsertTestResource(t, st, "aws", acct.ID, TypeEntityResolutionPolicyStatement, mwARN+"/policy", testRegion, "{}")
	psIMW := upsertTestResource(t, st, "aws", acct.ID, TypeEntityResolutionPolicyStatement, imwARN+"/policy", testRegion, "{}")

	if err := resolveEntityResolutionPolicyStatementToParent(acct, st); err != nil {
		t.Fatalf("resolveEntityResolutionPolicyStatementToParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(psMW)
	assertRelationship(t, rels, psMW, mwID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(psIMW)
	assertRelationship(t, rels, psIMW, imwID, store.RelAttachedTo)
}
