package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

const testInspector2Member = "444444444444"

// TestResolveInspector2MemberOrgAccount_HappyPath verifies that the
// member row links to the Organizations account when both are scanned.
func TestResolveInspector2MemberOrgAccount_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	memberNativeID := inspector2MemberNativeID(testRegion, testAccountID, testInspector2Member)
	memberAttrs := fmt.Sprintf(`{"AccountId":%q,"RelationshipStatus":"ENABLED"}`, testInspector2Member)
	memberID := upsertTestResource(t, st, "aws", acct.ID, TypeInspector2Member, memberNativeID, testRegion, memberAttrs)

	orgAcctARN := fmt.Sprintf("arn:aws:organizations::%s:account/o-test/%s", testAccountID, testInspector2Member)
	orgAcctAttrs := fmt.Sprintf(`{"Id":%q,"Arn":%q}`, testInspector2Member, orgAcctARN)
	orgAcctID := upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsAccount, orgAcctARN, "", orgAcctAttrs)

	if err := resolveInspector2MemberOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveInspector2MemberOrgAccount: %v", err)
	}
	rels, err := st.RelationshipsFrom(memberID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, memberID, orgAcctID, store.RelAttachedTo)
}

// TestResolveInspector2MemberOrgAccount_FKSafe verifies that members in
// a partial-coverage scan (no Org tree) skip without erroring.
func TestResolveInspector2MemberOrgAccount_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	memberNativeID := inspector2MemberNativeID(testRegion, testAccountID, testInspector2Member)
	memberAttrs := fmt.Sprintf(`{"AccountId":%q}`, testInspector2Member)
	memberID := upsertTestResource(t, st, "aws", acct.ID, TypeInspector2Member, memberNativeID, testRegion, memberAttrs)

	if err := resolveInspector2MemberOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveInspector2MemberOrgAccount: %v", err)
	}
	rels, err := st.RelationshipsFrom(memberID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges in FK-safe partial-coverage scan, got %d", len(rels))
	}
}

// TestResolveInspector2MemberOrgAccount_MalformedAttrs ensures invalid
// attrs JSON skips the row rather than aborting the resolver.
func TestResolveInspector2MemberOrgAccount_MalformedAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", acct.ID, TypeInspector2Member,
		inspector2MemberNativeID(testRegion, testAccountID, testInspector2Member),
		testRegion, `not json`)

	if err := resolveInspector2MemberOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveInspector2MemberOrgAccount: %v", err)
	}
}
