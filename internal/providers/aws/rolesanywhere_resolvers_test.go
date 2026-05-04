package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveRACRLToTrustAnchor(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	taARN := fmt.Sprintf("arn:aws:rolesanywhere:%s:%s:trust-anchor/ta-1", testRegion, acct.ID)
	taID := upsertTestResource(t, st, "aws", acct.ID, TypeRolesAnywhereTrustAnchor, taARN, testRegion, "{}")
	crlARN := fmt.Sprintf("arn:aws:rolesanywhere:%s:%s:crl/crl-1", testRegion, acct.ID)
	crlID := upsertTestResource(t, st, "aws", acct.ID, TypeRolesAnywhereCRL, crlARN, testRegion,
		fmt.Sprintf(`{"TrustAnchorArn":"%s"}`, taARN))
	if err := resolveRACRLToTrustAnchor(acct, st); err != nil {
		t.Fatalf("resolveRACRLToTrustAnchor: %v", err)
	}
	rels, _ := st.RelationshipsFrom(crlID)
	assertRelationship(t, rels, crlID, taID, store.RelAttachedTo)
}

func TestResolveRAProfileRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/ra-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	polARN := fmt.Sprintf("arn:aws:iam::%s:policy/ra-pol", acct.ID)
	polID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, polARN, "", "{}")
	pARN := fmt.Sprintf("arn:aws:rolesanywhere:%s:%s:profile/p-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"RoleArns":["%s"],"ManagedPolicyArns":["%s"]}`, roleARN, polARN)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeRolesAnywhereProfile, pARN, testRegion, attrs)
	if err := resolveRAProfileRefs(acct, st); err != nil {
		t.Fatalf("resolveRAProfileRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, roleID, store.RelUses)
	assertRelationship(t, rels, pID, polID, store.RelUses)
}
