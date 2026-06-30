package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestWisdomVersionParentARN(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:wisdom:us-east-1:123:assistant/abc/aiagent/x:5", "arn:aws:wisdom:us-east-1:123:assistant/abc/aiagent/x"},
		{"arn:aws:wisdom:us-east-1:123:assistant/abc/aiagent/x", ""},
		{"arn:aws:wisdom:us-east-1:123:assistant/abc/aiagent/x:notnum", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := wisdomVersionParentARN(c.in); got != c.want {
			t.Errorf("wisdomVersionParentARN(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveWisdomAssistantChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	asARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:assistant/abc", testRegion, acct.ID)
	asID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomAssistant, asARN, testRegion, "{}")
	agARN := asARN + "/aiagent/agent-1"
	attrs := fmt.Sprintf(`{"AssistantArn":%q}`, asARN)
	agID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomAIAgent, agARN, testRegion, attrs)
	if err := resolveWisdomAssistantChildren(acct, st); err != nil {
		t.Fatalf("resolveWisdomAssistantChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(agID)
	assertRelationship(t, rels, agID, asID, store.RelAttachedTo)
}

func TestResolveWisdomVersionParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	agARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:assistant/abc/aiagent/x", testRegion, acct.ID)
	agID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomAIAgent, agARN, testRegion, "{}")
	vARN := agARN + ":5"
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomAIAgentVersion, vARN, testRegion, "{}")
	if err := resolveWisdomVersionParent(acct, st); err != nil {
		t.Fatalf("resolveWisdomVersionParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, agID, store.RelAttachedTo)
}

func TestResolveWisdomAssistantAssociationKnowledgeBase(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	kbARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1", testRegion, acct.ID)
	kbID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomKnowledgeBase, kbARN, testRegion, "{}")
	aaARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:assistant/abc/association/aa-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"AssociationData":{"KnowledgeBaseAssociation":{"KnowledgeBaseArn":%q}}}`, kbARN)
	aaID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomAssistantAssociation, aaARN, testRegion, attrs)
	if err := resolveWisdomAssistantAssociationKnowledgeBase(acct, st); err != nil {
		t.Fatalf("resolveWisdomAssistantAssociationKnowledgeBase: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aaID)
	assertRelationship(t, rels, aaID, kbID, store.RelUses)
}

func TestResolveWisdomKnowledgeBaseChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	kbARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1", testRegion, acct.ID)
	kbID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomKnowledgeBase, kbARN, testRegion, "{}")
	mtARN := kbARN + "/messageTemplate/mt-1"
	attrs := fmt.Sprintf(`{"KnowledgeBaseArn":%q}`, kbARN)
	mtID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomMessageTemplate, mtARN, testRegion, attrs)
	if err := resolveWisdomKnowledgeBaseChildren(acct, st); err != nil {
		t.Fatalf("resolveWisdomKnowledgeBaseChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mtID)
	assertRelationship(t, rels, mtID, kbID, store.RelAttachedTo)
}

func TestResolveWisdomContentToKnowledgeBase(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	kbARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1", testRegion, acct.ID)
	kbID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomKnowledgeBase, kbARN, testRegion, "{}")
	cARN := kbARN + "/content/c-1"
	attrs := fmt.Sprintf(`{"KnowledgeBaseArn":%q}`, kbARN)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomContent, cARN, testRegion, attrs)
	if err := resolveWisdomKnowledgeBaseChildren(acct, st); err != nil {
		t.Fatalf("resolveWisdomKnowledgeBaseChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, kbID, store.RelAttachedTo)
}

func TestResolveWisdomContentToKnowledgeBase_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1/content/c-1", testRegion, acct.ID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomContent, cARN, testRegion, "{}")
	if err := resolveWisdomKnowledgeBaseChildren(acct, st); err != nil {
		t.Fatalf("resolveWisdomKnowledgeBaseChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	if len(rels) != 0 {
		t.Fatalf("expected no edges for content with no attrs, got %d", len(rels))
	}
}

func TestResolveWisdomContentAssociationParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1/content/c-1", testRegion, acct.ID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomContent, cARN, testRegion, "{}")
	caARN := cARN + "/association/ca-1"
	attrs := fmt.Sprintf(`{"ContentArn":%q}`, cARN)
	caID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomContentAssociation, caARN, testRegion, attrs)
	if err := resolveWisdomContentAssociationParent(acct, st); err != nil {
		t.Fatalf("resolveWisdomContentAssociationParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(caID)
	assertRelationship(t, rels, caID, cID, store.RelAttachedTo)
}

func TestResolveWisdomContentAssociationParent_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	caARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1/content/c-1/association/ca-1", testRegion, acct.ID)
	caID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomContentAssociation, caARN, testRegion, "{}")
	if err := resolveWisdomContentAssociationParent(acct, st); err != nil {
		t.Fatalf("resolveWisdomContentAssociationParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(caID)
	if len(rels) != 0 {
		t.Fatalf("expected no edges for content-association with no attrs, got %d", len(rels))
	}
}

func TestResolveWisdomKMSRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:assistant/a-1", testRegion, acct.ID)
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomAssistant, aARN, testRegion,
		fmt.Sprintf(`{"ServerSideEncryptionConfiguration":{"KmsKeyId":"%s"}}`, keyARN))
	kbARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1", testRegion, acct.ID)
	kbID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomKnowledgeBase, kbARN, testRegion,
		fmt.Sprintf(`{"ServerSideEncryptionConfiguration":{"KmsKeyId":"%s"}}`, keyARN))
	if err := resolveWisdomKMSRefs(acct, st); err != nil {
		t.Fatalf("resolveWisdomKMSRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, keyID, store.RelUses)
	rels, _ = st.RelationshipsFrom(kbID)
	assertRelationship(t, rels, kbID, keyID, store.RelUses)
}
