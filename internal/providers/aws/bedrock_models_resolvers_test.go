package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/icearp/disco-cli/store"
)

func bedrockAttrs(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal bedrock attrs: %v", err)
	}
	return string(b)
}

func TestResolveBedrockModelServes(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cmArn := fmt.Sprintf("arn:aws:bedrock:%s:%s:custom-model/cm-1", testRegion, testAccountID)
	cmID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockCustomModel, cmArn, testRegion, "{}")

	dpArn := fmt.Sprintf("arn:aws:bedrock:%s:%s:custom-model-deployment/dp-1", testRegion, testAccountID)
	dpID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockCustomModelDeployment, dpArn, testRegion,
		bedrockAttrs(t, bedrocktypes.CustomModelDeploymentSummary{CustomModelDeploymentArn: &dpArn, ModelArn: &cmArn}))

	pmArn := fmt.Sprintf("arn:aws:bedrock:%s:%s:provisioned-model/pm-1", testRegion, testAccountID)
	pmID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockProvisionedModel, pmArn, testRegion,
		bedrockAttrs(t, bedrocktypes.ProvisionedModelSummary{ProvisionedModelArn: &pmArn, ModelArn: &cmArn}))

	if err := resolveBedrockModelServes(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	dpRels, _ := st.RelationshipsFrom(dpID)
	assertRelationship(t, dpRels, dpID, cmID, store.RelUses)
	pmRels, _ := st.RelationshipsFrom(pmID)
	assertRelationship(t, pmRels, pmID, cmID, store.RelUses)
}

// A deployment/throughput serving a base foundation model (not a scanned custom
// model), and one with empty attrs, emit no edge.
func TestResolveBedrockModelServes_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Seed a custom model so the set is non-empty.
	cmArn := fmt.Sprintf("arn:aws:bedrock:%s:%s:custom-model/cm-present", testRegion, testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeBedrockCustomModel, cmArn, testRegion, "{}")

	fmArn := "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-v2"
	dpArn := fmt.Sprintf("arn:aws:bedrock:%s:%s:custom-model-deployment/dp-fm", testRegion, testAccountID)
	dpID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockCustomModelDeployment, dpArn, testRegion,
		bedrockAttrs(t, bedrocktypes.CustomModelDeploymentSummary{CustomModelDeploymentArn: &dpArn, ModelArn: &fmArn}))

	emptyArn := fmt.Sprintf("arn:aws:bedrock:%s:%s:provisioned-model/pm-empty", testRegion, testAccountID)
	emptyID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockProvisionedModel, emptyArn, testRegion, "{}")

	if err := resolveBedrockModelServes(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, id := range []string{dpID, emptyID} {
		rels, _ := st.RelationshipsFrom(id)
		if len(rels) != 0 {
			t.Errorf("row %s emitted %d edges, want 0", id, len(rels))
		}
	}
}
