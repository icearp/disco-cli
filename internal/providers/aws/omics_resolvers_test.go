package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveOmicsWorkflowVersionParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	wARN := fmt.Sprintf("arn:aws:omics:%s:%s:workflow/w1", testRegion, acct.ID)
	wID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsWorkflow, wARN, testRegion, `{"Id":"w1"}`)
	vARN := wARN + "/version/v1"
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsWorkflowVersion, vARN, testRegion, `{"WorkflowId":"w1"}`)
	if err := resolveOmicsWorkflowVersionParent(acct, st); err != nil {
		t.Fatalf("resolveOmicsWorkflowVersionParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, wID, store.RelAttachedTo)
}
