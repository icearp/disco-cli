package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveSignerProfilePermissionToProfile(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pARN := fmt.Sprintf("arn:aws:signer:%s:%s:/signing-profiles/myprofile", testRegion, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeSignerSigningProfile, pARN, testRegion, "{}")
	permARN := pARN + "/permission/stmt-1"
	permID := upsertTestResource(t, st, "aws", acct.ID, TypeSignerProfilePermission, permARN, testRegion, "{}")
	if err := resolveSignerProfilePermissionToProfile(acct, st); err != nil {
		t.Fatalf("resolveSignerProfilePermissionToProfile: %v", err)
	}
	rels, _ := st.RelationshipsFrom(permID)
	assertRelationship(t, rels, permID, pID, store.RelAttachedTo)
}
