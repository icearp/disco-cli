package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

// TestResolveBedrockAgentRefs covers both the bare-ID and full-ARN forms
// of GuardrailConfiguration.GuardrailIdentifier.
func TestResolveBedrockAgentRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gARN := bedrockGuardrailARN(testRegion, acct.ID, "g-1")
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockGuardrail, gARN, testRegion, "{}")
	agentARN := bedrockAgentARN(testRegion, acct.ID, "agent-1")
	agentID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgent, agentARN, testRegion,
		`{"AgentId":"agent-1","GuardrailConfiguration":{"GuardrailIdentifier":"g-1"}}`)

	if err := resolveBedrockAgentRefs(acct, st); err != nil {
		t.Fatalf("resolveBedrockAgentRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(agentID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, agentID, gID, store.RelUses)
}

func TestResolveBedrockAgentRefs_UnscannedSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	agentARN := bedrockAgentARN(testRegion, acct.ID, "agent-1")
	agentID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgent, agentARN, testRegion,
		`{"GuardrailConfiguration":{"GuardrailIdentifier":"missing"}}`)
	if err := resolveBedrockAgentRefs(acct, st); err != nil {
		t.Fatalf("resolveBedrockAgentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(agentID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveBedrockAgentAlias(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	agentARN := bedrockAgentARN(testRegion, acct.ID, "agent-1")
	agentRowID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgent, agentARN, testRegion, "{}")
	aliasARN := bedrockAgentAliasARN(testRegion, acct.ID, "agent-1", "alias-1")
	aliasID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentAlias, aliasARN, testRegion,
		`{"AgentAliasId":"alias-1"}`)

	if err := resolveBedrockAgentAlias(acct, st); err != nil {
		t.Fatalf("resolveBedrockAgentAlias: %v", err)
	}
	rels, err := st.RelationshipsFrom(aliasID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, aliasID, agentRowID, store.RelAttachedTo)
}

func TestResolveBedrockAgentAlias_UnscannedSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	aliasARN := bedrockAgentAliasARN(testRegion, acct.ID, "agent-x", "alias-1")
	aliasID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentAlias, aliasARN, testRegion, "{}")
	if err := resolveBedrockAgentAlias(acct, st); err != nil {
		t.Fatalf("resolveBedrockAgentAlias: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aliasID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveBedrockDataSourceKB(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	kbARN := bedrockKBARN(testRegion, acct.ID, "kb-1")
	kbID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockKnowledgeBase, kbARN, testRegion, "{}")
	dsARN := bedrockDataSourceARN(testRegion, acct.ID, "kb-1", "ds-1")
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockDataSource, dsARN, testRegion,
		`{"DataSourceId":"ds-1","KnowledgeBaseId":"kb-1"}`)

	if err := resolveBedrockDataSourceKB(acct, st); err != nil {
		t.Fatalf("resolveBedrockDataSourceKB: %v", err)
	}
	rels, err := st.RelationshipsFrom(dsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, dsID, kbID, store.RelAttachedTo)
}

func TestResolveBedrockDataSourceKB_UnscannedSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dsARN := bedrockDataSourceARN(testRegion, acct.ID, "kb-missing", "ds-1")
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockDataSource, dsARN, testRegion,
		`{"KnowledgeBaseId":"kb-missing"}`)
	if err := resolveBedrockDataSourceKB(acct, st); err != nil {
		t.Fatalf("resolveBedrockDataSourceKB: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dsID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveBedrockGuardrailVersion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gARN := bedrockGuardrailARN(testRegion, acct.ID, "g-1")
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockGuardrail, gARN, testRegion, "{}")
	gvNative := gARN + ":1"
	gvID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockGuardrailVersion, gvNative, testRegion,
		`{"Version":"1"}`)

	if err := resolveBedrockGuardrailVersion(acct, st); err != nil {
		t.Fatalf("resolveBedrockGuardrailVersion: %v", err)
	}
	rels, err := st.RelationshipsFrom(gvID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, gvID, gID, store.RelAttachedTo)
}

func TestResolveBedrockGuardrailVersion_UnscannedSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gvNative := bedrockGuardrailARN(testRegion, acct.ID, "missing") + ":1"
	gvID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockGuardrailVersion, gvNative, testRegion, "{}")
	if err := resolveBedrockGuardrailVersion(acct, st); err != nil {
		t.Fatalf("resolveBedrockGuardrailVersion: %v", err)
	}
	rels, _ := st.RelationshipsFrom(gvID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveBedrockPromptVersion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	promptARN := fmt.Sprintf("arn:aws:bedrock:%s:%s:prompt/PROMPT1", testRegion, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockPrompt, promptARN, testRegion, "{}")
	pvNative := promptARN + ":1"
	pvID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockPromptVersion, pvNative, testRegion,
		`{"Version":"1"}`)

	if err := resolveBedrockPromptVersion(acct, st); err != nil {
		t.Fatalf("resolveBedrockPromptVersion: %v", err)
	}
	rels, err := st.RelationshipsFrom(pvID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, pvID, pID, store.RelAttachedTo)
}

func TestResolveBedrockPromptVersion_UnscannedSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pvNative := fmt.Sprintf("arn:aws:bedrock:%s:%s:prompt/MISSING:1", testRegion, acct.ID)
	pvID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockPromptVersion, pvNative, testRegion, "{}")
	if err := resolveBedrockPromptVersion(acct, st); err != nil {
		t.Fatalf("resolveBedrockPromptVersion: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pvID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveBedrockFlowAlias(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	flowARN := fmt.Sprintf("arn:aws:bedrock:%s:%s:flow/FLOW1", testRegion, acct.ID)
	flowID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockFlow, flowARN, testRegion,
		`{"Id":"FLOW1","Arn":"`+flowARN+`"}`)
	aliasARN := flowARN + "/alias/ALIAS1"
	aliasID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockFlowAlias, aliasARN, testRegion,
		`{"FlowId":"FLOW1","Id":"ALIAS1"}`)

	if err := resolveBedrockFlowAlias(acct, st); err != nil {
		t.Fatalf("resolveBedrockFlowAlias: %v", err)
	}
	rels, err := st.RelationshipsFrom(aliasID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, aliasID, flowID, store.RelAttachedTo)
}

func TestResolveBedrockFlowAlias_UnscannedSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	aliasARN := fmt.Sprintf("arn:aws:bedrock:%s:%s:flow/MISSING/alias/ALIAS1", testRegion, acct.ID)
	aliasID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockFlowAlias, aliasARN, testRegion,
		`{"FlowId":"MISSING"}`)
	if err := resolveBedrockFlowAlias(acct, st); err != nil {
		t.Fatalf("resolveBedrockFlowAlias: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aliasID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveBedrockFlowVersion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	flowARN := fmt.Sprintf("arn:aws:bedrock:%s:%s:flow/FLOW1", testRegion, acct.ID)
	flowID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockFlow, flowARN, testRegion,
		`{"Id":"FLOW1"}`)
	versionARN := flowARN + "/version/1"
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockFlowVersion, versionARN, testRegion,
		`{"Id":"FLOW1","Version":"1"}`)

	if err := resolveBedrockFlowVersion(acct, st); err != nil {
		t.Fatalf("resolveBedrockFlowVersion: %v", err)
	}
	rels, err := st.RelationshipsFrom(vID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, vID, flowID, store.RelAttachedTo)
}

func TestResolveBedrockFlowVersion_UnscannedSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	versionARN := fmt.Sprintf("arn:aws:bedrock:%s:%s:flow/MISSING/version/1", testRegion, acct.ID)
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockFlowVersion, versionARN, testRegion,
		`{"Id":"MISSING","Version":"1"}`)
	if err := resolveBedrockFlowVersion(acct, st); err != nil {
		t.Fatalf("resolveBedrockFlowVersion: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveBedrockARPolicyVersion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pARN := fmt.Sprintf("arn:aws:bedrock:%s:%s:automated-reasoning-policy/p-1", testRegion, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAutomatedReasoningPolicy, pARN, testRegion, "{}")
	vARN := pARN + ":1"
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAutomatedReasoningPolicyVersion, vARN, testRegion, "{}")
	if err := resolveBedrockARPolicyVersion(acct, st); err != nil {
		t.Fatalf("resolveBedrockARPolicyVersion: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, pID, store.RelAttachedTo)
}

func TestResolveBedrockKBStorageRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/kb-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	cARN := fmt.Sprintf("arn:aws:aoss:%s:%s:collection/c-1", testRegion, acct.ID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeOSSCollection, cARN, testRegion, "{}")
	kbARN := fmt.Sprintf("arn:aws:bedrock:%s:%s:knowledge-base/kb-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"RoleArn":"%s","StorageConfiguration":{"OpensearchServerlessConfiguration":{"CollectionArn":"%s"}}}`, roleARN, cARN)
	kbID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockKnowledgeBase, kbARN, testRegion, attrs)
	if err := resolveBedrockKBStorageRefs(acct, st); err != nil {
		t.Fatalf("resolveBedrockKBStorageRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(kbID)
	assertRelationship(t, rels, kbID, roleID, store.RelUses)
	assertRelationship(t, rels, kbID, cID, store.RelUses)
}

func TestResolveBedrockDataSourceRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bARN := "arn:aws:s3:::my-bucket"
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, testRegion, "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	dsARN := fmt.Sprintf("arn:aws:bedrock:%s:%s:knowledge-base/kb-1/data-source/ds-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"DataSourceConfiguration":{"S3Configuration":{"BucketArn":"%s"}},"ServerSideEncryptionConfiguration":{"KmsKeyArn":"%s"}}`, bARN, keyARN)
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockDataSource, dsARN, testRegion, attrs)
	if err := resolveBedrockDataSourceRefs(acct, st); err != nil {
		t.Fatalf("resolveBedrockDataSourceRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, rels, dsID, bID, store.RelUses)
	assertRelationship(t, rels, dsID, keyID, store.RelUses)
}
