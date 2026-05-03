package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveCodePipelineWebhookToPipeline(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pARN := fmt.Sprintf("arn:aws:codepipeline:%s:%s:p1", testRegion, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeCodePipelinePipeline, pARN, testRegion, "{}")
	whARN := fmt.Sprintf("arn:aws:codepipeline:%s:%s:webhook:wh1", testRegion, acct.ID)
	whID := upsertTestResource(t, st, "aws", acct.ID, TypeCodePipelineWebhook, whARN, testRegion, `{"Definition":{"TargetPipeline":"p1"}}`)
	if err := resolveCodePipelineWebhookToPipeline(acct, st); err != nil {
		t.Fatalf("resolveCodePipelineWebhookToPipeline: %v", err)
	}
	rels, _ := st.RelationshipsFrom(whID)
	assertRelationship(t, rels, whID, pID, store.RelAttachedTo)
}
