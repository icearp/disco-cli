package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestIDStoreMembershipParse(t *testing.T) {
	owner, store := idStoreMembershipParse("arn:aws:identitystore::123:membership/d-abc/m-xyz")
	if owner != "123" || store != "d-abc" {
		t.Errorf("parse=%q,%q want 123,d-abc", owner, store)
	}
}

func TestResolveIdentityStoreGroupMembershipRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	owner := acct.ID
	idStore := "d-abc"
	uARN := identityStoreUserNativeID(owner, idStore, "u-1")
	uID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreUser, uARN, testRegion, "{}")
	gARN := identityStoreGroupNativeID(owner, idStore, "g-1")
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreGroup, gARN, testRegion, "{}")

	mARN := fmt.Sprintf("arn:aws:identitystore::%s:membership/%s/m-1", owner, idStore)
	mAttrs := `{"GroupId":"g-1","MemberId":{"Value":"u-1"}}`
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreGroupMembership, mARN, testRegion, mAttrs)

	if err := resolveIdentityStoreGroupMembershipRefs(acct, st); err != nil {
		t.Fatalf("resolveIdentityStoreGroupMembershipRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mID)
	assertRelationship(t, rels, mID, gID, store.RelAttachedTo)
	assertRelationship(t, rels, mID, uID, store.RelAttachedTo)
}
