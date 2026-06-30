package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
	osistypes "github.com/aws/aws-sdk-go-v2/service/osis/types"
)

func TestResolveOSISPipelineEndpointPipeline(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pipeARN := fmt.Sprintf("arn:aws:osis:%s:%s:pipeline/my-pipe", testRegion, acct.ID)
	pipeID := upsertTestResource(t, st, "aws", acct.ID, TypeOSISPipeline, pipeARN, testRegion, "{}")
	epNativeID := fmt.Sprintf("arn:aws:osis:%s:%s:pipeline-endpoint/ep-1", testRegion, acct.ID)
	epAttrs := mustJSON(osistypes.PipelineEndpoint{EndpointId: sp("ep-1"), PipelineArn: &pipeARN})
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeOSISPipelineEndpoint, epNativeID, testRegion, epAttrs)
	if err := resolveOSISPipelineEndpointPipeline(acct, st); err != nil {
		t.Fatalf("resolveOSISPipelineEndpointPipeline: %v", err)
	}
	rels, _ := st.RelationshipsFrom(epID)
	assertRelationship(t, rels, epID, pipeID, store.RelAttachedTo)
}

func TestResolveOSISPipelineEndpointPipeline_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	epNativeID := fmt.Sprintf("arn:aws:osis:%s:%s:pipeline-endpoint/ep-1", testRegion, acct.ID)
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeOSISPipelineEndpoint, epNativeID, testRegion, "{}")
	if err := resolveOSISPipelineEndpointPipeline(acct, st); err != nil {
		t.Fatalf("resolveOSISPipelineEndpointPipeline: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(epID); len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}
