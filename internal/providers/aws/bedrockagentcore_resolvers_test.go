package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveBACGatewayTargetParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gwARN := fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:gateway/g1", testRegion, acct.ID)
	gwID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreGateway, gwARN, testRegion, "{}")
	tARN := fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:gateway-target/g1/t1", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreGatewayTarget, tARN, testRegion, "{}")
	if err := resolveBACGatewayTargetParent(acct, st); err != nil {
		t.Fatalf("resolveBACGatewayTargetParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tID)
	assertRelationship(t, rels, tID, gwID, store.RelAttachedTo)
}

func TestResolveBACRuntimeEndpointParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rtARN := fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:runtime/rt1", testRegion, acct.ID)
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreRuntime, rtARN, testRegion, "{}")
	eARN := fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:runtime-endpoint/rt1/e1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"AgentRuntimeArn":%q}`, rtARN)
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreRuntimeEndpoint, eARN, testRegion, attrs)
	if err := resolveBACRuntimeEndpointParent(acct, st); err != nil {
		t.Fatalf("resolveBACRuntimeEndpointParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eID)
	assertRelationship(t, rels, eID, rtID, store.RelAttachedTo)
}

func TestResolveBACPolicyEngine(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	peARN := fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:policy-engine/pe1", testRegion, acct.ID)
	peID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCorePolicyEngine, peARN, testRegion, `{"PolicyEngineId":"pe1"}`)
	pARN := fmt.Sprintf("arn:aws:bedrock-agentcore:%s:%s:policy/p1", testRegion, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCorePolicy, pARN, testRegion, `{"PolicyEngineId":"pe1"}`)
	if err := resolveBACPolicyEngine(acct, st); err != nil {
		t.Fatalf("resolveBACPolicyEngine: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, peID, store.RelAttachedTo)
}
