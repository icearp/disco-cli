package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveVPChildToPolicyStore(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	psARN := fmt.Sprintf("arn:aws:verifiedpermissions::%s:policy-store/abc", acct.ID)
	psID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsPolicyStore, psARN, testRegion, "{}")
	pARN := psARN + "/policy/p1"
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsPolicy, pARN, testRegion, "{}")
	tARN := psARN + "/policy-template/t1"
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsPolicyTemplate, tARN, testRegion, "{}")
	isARN := psARN + "/identity-source/i1"
	isID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsIdentitySource, isARN, testRegion, "{}")
	if err := resolveVPChildToPolicyStore(acct, st); err != nil {
		t.Fatalf("resolveVPChildToPolicyStore: %v", err)
	}
	for _, srcID := range []string{pID, tID, isID} {
		rels, _ := st.RelationshipsFrom(srcID)
		assertRelationship(t, rels, srcID, psID, store.RelAttachedTo)
	}
}
