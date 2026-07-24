package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveAidevopsRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion,
		fmt.Sprintf(`{"KeyId":"abc-123","Arn":%q}`, keyARN))

	vpcID := "vpc-aaa"
	vpcRowID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", vpcID), testRegion, "{}")

	spaceNative := aidevopsARN(testRegion, acct.ID, "agent-space", "space-1")
	spaceID := upsertTestResource(t, st, "aws", acct.ID, TypeAidevopsAgentSpace, spaceNative, testRegion,
		fmt.Sprintf(`{"AgentSpaceId":"space-1","KmsKeyArn":%q}`, keyARN))

	svcNative := aidevopsARN(testRegion, acct.ID, "service", "svc-1")
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeAidevopsService, svcNative, testRegion,
		fmt.Sprintf(`{"ServiceId":"svc-1","KmsKeyArn":%q}`, keyARN))

	assocNative := aidevopsARN(testRegion, acct.ID, "association", "assoc-1")
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeAidevopsAssociations, assocNative, testRegion,
		`{"AssociationId":"assoc-1","AgentSpaceId":"space-1","ServiceId":"svc-1"}`)

	pcNative := aidevopsARN(testRegion, acct.ID, "private-connection", "pc-1")
	pcID := upsertTestResource(t, st, "aws", acct.ID, TypeAidevopsPrivateConnection, pcNative, testRegion,
		fmt.Sprintf(`{"Name":"pc-1","VpcId":%q}`, vpcID))

	if err := resolveAidevopsRefs(acct, st); err != nil {
		t.Fatalf("resolveAidevopsRefs: %v", err)
	}

	spaceRels, _ := st.RelationshipsFrom(spaceID)
	assertRelationship(t, spaceRels, spaceID, keyID, store.RelUses)
	svcRels, _ := st.RelationshipsFrom(svcID)
	assertRelationship(t, svcRels, svcID, keyID, store.RelUses)
	assocRels, _ := st.RelationshipsFrom(assocID)
	assertRelationship(t, assocRels, assocID, spaceID, store.RelAttachedTo)
	assertRelationship(t, assocRels, assocID, svcID, store.RelUses)
	pcRels, _ := st.RelationshipsFrom(pcID)
	assertRelationship(t, pcRels, pcID, vpcRowID, store.RelAttachedTo)
}

// TestResolveAidevopsRefs_NoAttrs guards the empty-attrs path: rows with no
// cross-resource fields must produce no edges and no panic.
func TestResolveAidevopsRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	spaceNative := aidevopsARN(testRegion, acct.ID, "agent-space", "space-1")
	spaceID := upsertTestResource(t, st, "aws", acct.ID, TypeAidevopsAgentSpace, spaceNative, testRegion, "{}")
	assocNative := aidevopsARN(testRegion, acct.ID, "association", "assoc-1")
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeAidevopsAssociations, assocNative, testRegion, "{}")

	if err := resolveAidevopsRefs(acct, st); err != nil {
		t.Fatalf("resolveAidevopsRefs (no attrs): %v", err)
	}
	if rels, _ := st.RelationshipsFrom(spaceID); len(rels) != 0 {
		t.Errorf("expected no edges from bare agent-space, got %d", len(rels))
	}
	if rels, _ := st.RelationshipsFrom(assocID); len(rels) != 0 {
		t.Errorf("expected no edges from bare association, got %d", len(rels))
	}
}
