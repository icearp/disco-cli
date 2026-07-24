package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveWAFRegionalWebACLAssociations(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	webaclARN := wafRegionalARN(testRegion, acct.ID, "webacl", "acl-1")
	webaclID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFRegionalWebACL, webaclARN, testRegion,
		`{"WebACLId":"acl-1","Name":"my-acl"}`)

	albARN := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/app/my-alb/abc123", testRegion, acct.ID)
	assocNative := webaclARN + "/association/abc123"
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFRegionalWebACLAssociation, assocNative, testRegion,
		fmt.Sprintf(`{"WebACLId":"acl-1","ResourceArn":%q,"ResourceType":"APPLICATION_LOAD_BALANCER"}`, albARN))

	if err := resolveWAFRegionalWebACLAssociations(acct, st); err != nil {
		t.Fatalf("resolveWAFRegionalWebACLAssociations: %v", err)
	}

	rels, _ := st.RelationshipsFrom(assocID)
	assertRelationship(t, rels, assocID, webaclID, store.RelAttachedTo)
}

// TestResolveWAFRegionalWebACLAssociations_NoAttrs guards the empty-attrs path:
// an association row with no WebACLId must produce no edge and no panic.
func TestResolveWAFRegionalWebACLAssociations_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	webaclARN := wafRegionalARN(testRegion, acct.ID, "webacl", "acl-1")
	assocNative := webaclARN + "/association/abc123"
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeWAFRegionalWebACLAssociation, assocNative, testRegion, "{}")

	if err := resolveWAFRegionalWebACLAssociations(acct, st); err != nil {
		t.Fatalf("resolveWAFRegionalWebACLAssociations (no attrs): %v", err)
	}
	if rels, _ := st.RelationshipsFrom(assocID); len(rels) != 0 {
		t.Errorf("expected no edges from bare association, got %d", len(rels))
	}
}
