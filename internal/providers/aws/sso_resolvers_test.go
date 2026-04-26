package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const (
	testSSOInsID   = "ssoins-1234567890abcdef"
	testIdentityID = "d-1234567890"
	testOwnerAcct  = "111111111111"
	testTargetAcct = "222222222222"
	testPSId       = "ps-aaaa1111bbbb2222"
)

func ssoInstanceARN() string { return "arn:aws:sso:::instance/" + testSSOInsID }
func ssoPermSetARN() string  { return "arn:aws:sso:::permissionSet/" + testSSOInsID + "/" + testPSId }
func ssoInstanceAttrs() string {
	return fmt.Sprintf(`{"InstanceArn":%q,"IdentityStoreId":%q,"OwnerAccountId":%q,"Name":"main"}`,
		ssoInstanceARN(), testIdentityID, testOwnerAcct)
}

func TestInstanceArnFromPermissionSetArn(t *testing.T) {
	got := instanceArnFromPermissionSetArn(ssoPermSetARN())
	want := ssoInstanceARN()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if instanceArnFromPermissionSetArn("not an arn") != "" {
		t.Errorf("expected empty for malformed input")
	}
}

// TestResolveSSOPermissionSetInstance verifies the contains edge from
// instance to permission-set lands when both are scanned.
func TestResolveSSOPermissionSetInstance(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	insID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOInstance, ssoInstanceARN(), testRegion, ssoInstanceAttrs())
	psID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOPermissionSet, ssoPermSetARN(), testRegion, `{}`)

	if err := resolveSSOPermissionSetInstance(acct, st); err != nil {
		t.Fatalf("resolveSSOPermissionSetInstance: %v", err)
	}
	rels, err := st.RelationshipsFrom(insID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, insID, psID, store.RelContains)
}

// TestResolveSSOAccountAssignments_UserPath covers the principal=USER
// branch: the assignment links to the permission-set, identity-store user,
// and Organizations account targets when all three are scanned.
func TestResolveSSOAccountAssignments_UserPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", acct.ID, TypeSSOInstance, ssoInstanceARN(), testRegion, ssoInstanceAttrs())
	psID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOPermissionSet, ssoPermSetARN(), testRegion, `{}`)

	userPID := "user-uuid-1"
	userARN := identityStoreUserNativeID(testOwnerAcct, testIdentityID, userPID)
	userID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreUser, userARN, testRegion, `{}`)

	// Organizations account: NativeID = full ARN per CLAUDE.md.
	orgAcctARN := fmt.Sprintf("arn:aws:organizations::%s:account/o-test/%s", testOwnerAcct, testTargetAcct)
	orgAcctAttrs := fmt.Sprintf(`{"Id":%q,"Arn":%q}`, testTargetAcct, orgAcctARN)
	orgAcctID := upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsAccount, orgAcctARN, "", orgAcctAttrs)

	assignNativeID := ssoAssignmentNativeID(ssoPermSetARN(), testTargetAcct, "USER", userPID)
	assignAttrs := fmt.Sprintf(`{"AccountId":%q,"PermissionSetArn":%q,"PrincipalId":%q,"PrincipalType":"USER"}`,
		testTargetAcct, ssoPermSetARN(), userPID)
	assignID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOAccountAssignment, assignNativeID, testRegion, assignAttrs)

	if err := resolveSSOAccountAssignments(acct, st); err != nil {
		t.Fatalf("resolveSSOAccountAssignments: %v", err)
	}
	rels, err := st.RelationshipsFrom(assignID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, assignID, psID, store.RelUses)
	assertRelationship(t, rels, assignID, userID, store.RelUses)
	assertRelationship(t, rels, assignID, orgAcctID, store.RelAttachedTo)
}

// TestResolveSSOAccountAssignments_GroupPath covers principal=GROUP and
// verifies cross-account: assignment grants access to a target AWS account
// distinct from the assignment's own row account.
func TestResolveSSOAccountAssignments_GroupPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", acct.ID, TypeSSOInstance, ssoInstanceARN(), testRegion, ssoInstanceAttrs())
	upsertTestResource(t, st, "aws", acct.ID, TypeSSOPermissionSet, ssoPermSetARN(), testRegion, `{}`)

	groupPID := "group-uuid-2"
	groupARN := identityStoreGroupNativeID(testOwnerAcct, testIdentityID, groupPID)
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeIdentityStoreGroup, groupARN, testRegion, `{}`)

	assignNativeID := ssoAssignmentNativeID(ssoPermSetARN(), testTargetAcct, "GROUP", groupPID)
	assignAttrs := fmt.Sprintf(`{"AccountId":%q,"PermissionSetArn":%q,"PrincipalId":%q,"PrincipalType":"GROUP"}`,
		testTargetAcct, ssoPermSetARN(), groupPID)
	assignID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOAccountAssignment, assignNativeID, testRegion, assignAttrs)

	if err := resolveSSOAccountAssignments(acct, st); err != nil {
		t.Fatalf("resolveSSOAccountAssignments: %v", err)
	}
	rels, err := st.RelationshipsFrom(assignID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, assignID, groupID, store.RelUses)
}

// TestResolveSSOAccountAssignments_FKSafe verifies missing targets skip
// without erroring. Tests the partial-coverage scenario: assignments
// scanned but the corresponding identity-store / org tree not.
func TestResolveSSOAccountAssignments_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", acct.ID, TypeSSOInstance, ssoInstanceARN(), testRegion, ssoInstanceAttrs())
	// no permission-set, no user/group, no Org tree

	assignNativeID := ssoAssignmentNativeID(ssoPermSetARN(), testTargetAcct, "USER", "ghost")
	assignAttrs := fmt.Sprintf(`{"AccountId":%q,"PermissionSetArn":%q,"PrincipalId":"ghost","PrincipalType":"USER"}`,
		testTargetAcct, ssoPermSetARN())
	assignID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOAccountAssignment, assignNativeID, testRegion, assignAttrs)

	if err := resolveSSOAccountAssignments(acct, st); err != nil {
		t.Fatalf("resolveSSOAccountAssignments: %v", err)
	}
	rels, err := st.RelationshipsFrom(assignID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges in FK-safe partial-coverage scan, got %d", len(rels))
	}
}

// TestResolveSSOAccountAssignments_MalformedAttrs ensures invalid attrs
// JSON skips the row rather than aborting the whole resolver.
func TestResolveSSOAccountAssignments_MalformedAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", acct.ID, TypeSSOAccountAssignment,
		ssoAssignmentNativeID(ssoPermSetARN(), testTargetAcct, "USER", "x"),
		testRegion, `not json`)

	if err := resolveSSOAccountAssignments(acct, st); err != nil {
		t.Fatalf("resolveSSOAccountAssignments: %v", err)
	}
}
