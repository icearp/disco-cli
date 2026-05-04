package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const (
	testDetectiveGraphID    = "abc123def456abc123def456abc12345"
	testDetectiveMemberAcct = "333333333333"
)

func detectiveGraphARN() string {
	return fmt.Sprintf("arn:aws:detective:%s:%s:graph:%s", testRegion, testAccountID, testDetectiveGraphID)
}

// TestResolveDetectiveMemberOrgAccount_HappyPath verifies that the member
// row links to the Organizations account when both are scanned.
func TestResolveDetectiveMemberOrgAccount_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	graphARN := detectiveGraphARN()
	upsertTestResource(t, st, "aws", acct.ID, TypeDetectiveGraph, graphARN, testRegion, fmt.Sprintf(`{"Arn":%q}`, graphARN))

	memberNativeID := detectiveMemberNativeID(graphARN, testDetectiveMemberAcct)
	memberAttrs := fmt.Sprintf(`{"AccountId":%q,"GraphArn":%q,"Status":"ENABLED"}`, testDetectiveMemberAcct, graphARN)
	memberID := upsertTestResource(t, st, "aws", acct.ID, TypeDetectiveMember, memberNativeID, testRegion, memberAttrs)

	// Organizations account: NativeID = full ARN per CLAUDE.md "Org test fixtures".
	orgAcctARN := fmt.Sprintf("arn:aws:organizations::%s:account/o-test/%s", testAccountID, testDetectiveMemberAcct)
	orgAcctAttrs := fmt.Sprintf(`{"Id":%q,"Arn":%q}`, testDetectiveMemberAcct, orgAcctARN)
	orgAcctID := upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsAccount, orgAcctARN, "", orgAcctAttrs)

	if err := resolveDetectiveMemberOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveDetectiveMemberOrgAccount: %v", err)
	}
	rels, err := st.RelationshipsFrom(memberID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, memberID, orgAcctID, store.RelAttachedTo)
}

// TestResolveDetectiveMemberOrgAccount_FKSafe verifies that members in a
// partial-coverage scan (no Org tree) skip without erroring.
func TestResolveDetectiveMemberOrgAccount_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	graphARN := detectiveGraphARN()
	upsertTestResource(t, st, "aws", acct.ID, TypeDetectiveGraph, graphARN, testRegion, `{}`)

	memberNativeID := detectiveMemberNativeID(graphARN, testDetectiveMemberAcct)
	memberAttrs := fmt.Sprintf(`{"AccountId":%q}`, testDetectiveMemberAcct)
	memberID := upsertTestResource(t, st, "aws", acct.ID, TypeDetectiveMember, memberNativeID, testRegion, memberAttrs)

	if err := resolveDetectiveMemberOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveDetectiveMemberOrgAccount: %v", err)
	}
	rels, err := st.RelationshipsFrom(memberID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges in FK-safe partial-coverage scan, got %d", len(rels))
	}
}

// TestResolveDetectiveMemberOrgAccount_MalformedAttrs ensures invalid attrs
// JSON skips the row rather than aborting the resolver.
func TestResolveDetectiveMemberOrgAccount_MalformedAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	graphARN := detectiveGraphARN()
	upsertTestResource(t, st, "aws", acct.ID, TypeDetectiveMember,
		detectiveMemberNativeID(graphARN, testDetectiveMemberAcct),
		testRegion, `not json`)

	if err := resolveDetectiveMemberOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveDetectiveMemberOrgAccount: %v", err)
	}
}

func TestResolveDetectiveOrgAdminRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gARN := detectiveGraphARN()
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeDetectiveGraph, gARN, testRegion, "{}")
	orgARN := fmt.Sprintf("arn:aws:organizations::%s:account/o-1/210987654321", acct.ID)
	orgID := upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsAccount, orgARN, "",
		`{"Id":"210987654321","Arn":"`+orgARN+`"}`)
	oaARN := fmt.Sprintf("arn:aws:detective:%s:%s:organization-admin/210987654321", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"AccountId":"210987654321","GraphArn":"%s"}`, gARN)
	oaID := upsertTestResource(t, st, "aws", acct.ID, TypeDetectiveOrganizationAdmin, oaARN, testRegion, attrs)
	if err := resolveDetectiveOrgAdminRefs(acct, st); err != nil {
		t.Fatalf("resolveDetectiveOrgAdminRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(oaID)
	assertRelationship(t, rels, oaID, gID, store.RelAttachedTo)
	assertRelationship(t, rels, oaID, orgID, store.RelAttachedTo)
}
