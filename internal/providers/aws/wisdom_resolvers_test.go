package aws

import (
	"fmt"
	"testing"

	qconnecttypes "github.com/aws/aws-sdk-go-v2/service/qconnect/types"
	"github.com/icearp/disco-cli/store"
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

// wisdomTestKBAssociation is an assistant association to kbARN as the
// scanner stores it: the SDK summary marshalled, union and all.
func wisdomTestKBAssociation(kbARN string) string {
	return mustJSON(qconnecttypes.AssistantAssociationSummary{
		AssociationType: qconnecttypes.AssociationTypeKnowledgeBase,
		AssociationData: &qconnecttypes.AssistantAssociationOutputDataMemberKnowledgeBaseAssociation{
			Value: qconnecttypes.KnowledgeBaseAssociationData{KnowledgeBaseArn: ptrStr(kbARN), KnowledgeBaseId: ptrStr("kb-1")},
		},
	})
}

func TestResolveWisdomAssistantAssociationKnowledgeBase(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	kbARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1", testRegion, acct.ID)
	kbID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomKnowledgeBase, kbARN, testRegion, "{}")
	aaARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:assistant/abc/association/aa-1", testRegion, acct.ID)
	aaID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomAssistantAssociation, aaARN, testRegion, wisdomTestKBAssociation(kbARN))
	if err := resolveWisdomAssistantAssociationKnowledgeBase(acct, st); err != nil {
		t.Fatalf("resolveWisdomAssistantAssociationKnowledgeBase: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aaID)
	assertRelationship(t, rels, aaID, kbID, store.RelUses)
}

// TestResolveWisdomAssistantAssociationKnowledgeBase_NoEdge pins that an
// association with no attributes, or one to an external Bedrock knowledge base
// (the other AssociationData member), gets no edge to a knowledge base.
func TestResolveWisdomAssistantAssociationKnowledgeBase_NoEdge(t *testing.T) {
	kbARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1", testRegion, testAccountID)
	for _, c := range []struct{ name, attrs string }{
		{"no attrs", "{}"},
		{"external bedrock", mustJSON(qconnecttypes.AssistantAssociationSummary{
			AssociationType: qconnecttypes.AssociationTypeExternalBedrockKnowledgeBase,
			AssociationData: &qconnecttypes.AssistantAssociationOutputDataMemberExternalBedrockKnowledgeBaseConfig{
				Value: qconnecttypes.ExternalBedrockKnowledgeBaseConfig{BedrockKnowledgeBaseArn: ptrStr(kbARN), AccessRoleArn: ptrStr("arn:aws:iam::1:role/r")},
			},
		})},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			upsertTestResource(t, st, "aws", acct.ID, TypeWisdomKnowledgeBase, kbARN, testRegion, "{}")
			aaARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:assistant/abc/association/aa-1", testRegion, acct.ID)
			aaID := upsertTestResource(t, st, "aws", acct.ID, TypeWisdomAssistantAssociation, aaARN, testRegion, c.attrs)
			if err := resolveWisdomAssistantAssociationKnowledgeBase(acct, st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if rels, _ := st.RelationshipsFrom(aaID); len(rels) != 0 {
				t.Errorf("relationships = %v; want none", rels)
			}
		})
	}
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

// TestResolveWisdom_DecodeErrorReturned pins, for each resolver that decodes
// attributes, that a row whose attributes do not decode makes it return an
// error, and that the row read after it still gets its edge. ListResources
// orders by name, so the bad row "a" is read before the valid row "b".
func TestResolveWisdom_DecodeErrorReturned(t *testing.T) {
	asARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:assistant/abc", testRegion, testAccountID)
	kbARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1", testRegion, testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, testAccountID)
	notObject := `["not an object"]`
	for _, c := range []struct {
		name               string
		resolve            func(*account, *store.Store) error
		targetType, target string
		srcType, badAttrs  string
		goodNative, good   string
	}{
		{
			"kms", resolveWisdomKMSRefs, TypeKMSKey, keyARN, TypeWisdomAssistant, notObject,
			asARN + "/good", fmt.Sprintf(`{"ServerSideEncryptionConfiguration":{"KmsKeyId":%q}}`, keyARN),
		},
		{
			"child attrs", resolveWisdomAssistantChildren, TypeWisdomAssistant, asARN, TypeWisdomAIAgent, notObject,
			asARN + "/aiagent/good", fmt.Sprintf(`{"AssistantArn":%q}`, asARN),
		},
		{
			"child ARN not a string", resolveWisdomAssistantChildren, TypeWisdomAssistant, asARN, TypeWisdomAIAgent, `{"AssistantArn":5}`,
			asARN + "/aiagent/good", fmt.Sprintf(`{"AssistantArn":%q}`, asARN),
		},
		{
			"association", resolveWisdomAssistantAssociationKnowledgeBase, TypeWisdomKnowledgeBase, kbARN, TypeWisdomAssistantAssociation, notObject,
			asARN + "/association/good", wisdomTestKBAssociation(kbARN),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			tgtID := upsertTestResource(t, st, "aws", acct.ID, c.targetType, c.target, testRegion, "{}")
			upsertTestResourceNamed(t, st, c.srcType, asARN+"/bad", testRegion, c.badAttrs, "a")
			goodID := upsertTestResourceNamed(t, st, c.srcType, c.goodNative, testRegion, c.good, "b")
			if err := c.resolve(acct, st); err == nil {
				t.Fatal("resolve: nil error, want the decode error")
			}
			rels, _ := st.RelationshipsFrom(goodID)
			if len(rels) != 1 || rels[0].ToID != tgtID {
				t.Errorf("relationships of the valid row = %v; want one to %s", rels, c.target)
			}
		})
	}
}

// TestResolveWisdomChildren_DecodeErrorKeepsOtherTypes pins that a row of one
// child type that fails to decode costs the other child types none of their
// edges.
func TestResolveWisdomChildren_DecodeErrorKeepsOtherTypes(t *testing.T) {
	asARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:assistant/abc", testRegion, testAccountID)
	kbARN := fmt.Sprintf("arn:aws:wisdom:%s:%s:knowledge-base/kb-1", testRegion, testAccountID)
	for _, c := range []struct {
		name                       string
		resolve                    func(*account, *store.Store) error
		parentType, parent, field  string
		badType, goodType, goodARN string
	}{
		{"assistant", resolveWisdomAssistantChildren, TypeWisdomAssistant, asARN, "AssistantArn", TypeWisdomAssistantAssociation, TypeWisdomAIPrompt, asARN + "/aiprompt/p"},
		{"knowledge base", resolveWisdomKnowledgeBaseChildren, TypeWisdomKnowledgeBase, kbARN, "KnowledgeBaseArn", TypeWisdomMessageTemplate, TypeWisdomContent, kbARN + "/content/c"},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			pID := upsertTestResource(t, st, "aws", acct.ID, c.parentType, c.parent, testRegion, "{}")
			upsertTestResource(t, st, "aws", acct.ID, c.badType, c.parent+"/bad", testRegion, `["not an object"]`)
			goodID := upsertTestResource(t, st, "aws", acct.ID, c.goodType, c.goodARN, testRegion, fmt.Sprintf(`{%q:%q}`, c.field, c.parent))
			if err := c.resolve(acct, st); err == nil {
				t.Fatal("resolve: nil error, want the decode error")
			}
			rels, _ := st.RelationshipsFrom(goodID)
			assertRelationship(t, rels, goodID, pID, store.RelAttachedTo)
		})
	}
}
