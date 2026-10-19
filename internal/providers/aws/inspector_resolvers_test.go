package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	i2types "github.com/aws/aws-sdk-go-v2/service/inspector2/types"
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

// TestResolveInspector2MemberOrgAccount_MalformedAttrs pins that a member row
// whose attributes do not decode is returned as an error while every other
// member still gets its edge.
func TestResolveInspector2MemberOrgAccount_MalformedAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", acct.ID, TypeInspector2Member,
		inspector2MemberNativeID(testRegion, testAccountID, "888888888888"),
		testRegion, `not json`)
	b, err := json.Marshal(i2types.Member{AccountId: sdkaws.String(testInspector2Member)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	memberID := upsertTestResource(t, st, "aws", acct.ID, TypeInspector2Member,
		inspector2MemberNativeID(testRegion, testAccountID, testInspector2Member), testRegion, string(b))
	orgAcctID := seedI2OrgAccount(t, st, testInspector2Member)

	err = resolveInspector2MemberOrgAccount(acct, st)
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("err = %v; want the attributes decode error", err)
	}
	assertRelationship(t, kmsRelsFrom(t, st, memberID), memberID, orgAcctID, store.RelAttachedTo)
}

// seedI2DelegatedAdmin stores a delegated-admin row whose attributes are the
// marshalled SDK element, and returns its resource id.
func seedI2DelegatedAdmin(t *testing.T, st *store.Store, adminID string, el i2types.DelegatedAdminAccount) string {
	t.Helper()
	b, err := json.Marshal(el)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return upsertTestResource(t, st, "aws", testAccountID, TypeInspector2DelegatedAdminAccount,
		inspector2DelegatedAdminNativeID(testRegion, testAccountID, adminID), testRegion, string(b))
}

// seedI2OrgAccount stores an Organizations account row and returns its id.
func seedI2OrgAccount(t *testing.T, st *store.Store, accountID string) string {
	t.Helper()
	arn := fmt.Sprintf("arn:aws:organizations::%s:account/o-test/%s", testAccountID, accountID)
	return upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsAccount, arn, "",
		fmt.Sprintf(`{"Id":%q,"Arn":%q}`, accountID, arn))
}

func TestResolveInspector2DelegatedAdminOrgAccount_LinksOrgAccount(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	adminID := seedI2DelegatedAdmin(t, st, testInspector2Member, i2types.DelegatedAdminAccount{
		AccountId: sdkaws.String(testInspector2Member), Status: i2types.DelegatedAdminStatusEnabled,
	})
	outsideID := seedI2DelegatedAdmin(t, st, "777777777777", i2types.DelegatedAdminAccount{AccountId: sdkaws.String("777777777777")})
	orgAcctID := seedI2OrgAccount(t, st, testInspector2Member)

	if err := resolveInspector2DelegatedAdminOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveInspector2DelegatedAdminOrgAccount: %v", err)
	}
	rels := kmsRelsFrom(t, st, adminID)
	if len(rels) != 1 {
		t.Errorf("admin has %d edges; want 1", len(rels))
	}
	assertRelationship(t, rels, adminID, orgAcctID, store.RelAttachedTo)
	if got := kmsRelsFrom(t, st, outsideID); len(got) != 0 {
		t.Errorf("admin outside the scanned org has %d edges; want 0", len(got))
	}
}

func TestResolveInspector2DelegatedAdminOrgAccount_NoAccountID(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	adminID := seedI2DelegatedAdmin(t, st, testInspector2Member, i2types.DelegatedAdminAccount{})
	seedI2OrgAccount(t, st, testInspector2Member)

	if err := resolveInspector2DelegatedAdminOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveInspector2DelegatedAdminOrgAccount: %v", err)
	}
	if got := kmsRelsFrom(t, st, adminID); len(got) != 0 {
		t.Errorf("admin with no AccountId has %d edges; want 0", len(got))
	}
}

// TestResolveInspector2DelegatedAdminOrgAccount_NoOrgTree pins the
// partial-coverage short-circuit: no Organizations rows means no edges and no
// error, even for a row whose attributes would not decode.
func TestResolveInspector2DelegatedAdminOrgAccount_NoOrgTree(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	adminID := seedI2DelegatedAdmin(t, st, testInspector2Member, i2types.DelegatedAdminAccount{AccountId: sdkaws.String(testInspector2Member)})
	upsertTestResource(t, st, "aws", acct.ID, TypeInspector2DelegatedAdminAccount,
		inspector2DelegatedAdminNativeID(testRegion, testAccountID, "888888888888"), testRegion, `not json`)

	if err := resolveInspector2DelegatedAdminOrgAccount(acct, st); err != nil {
		t.Fatalf("resolveInspector2DelegatedAdminOrgAccount: %v", err)
	}
	if got := kmsRelsFrom(t, st, adminID); len(got) != 0 {
		t.Errorf("admin has %d edges with no org tree; want 0", len(got))
	}
}

// TestResolveInspector2DelegatedAdminOrgAccount_DecodeErrorKeepsOtherEdges
// pins that a row whose attributes do not decode is returned as an error
// while every other row still gets its edge.
func TestResolveInspector2DelegatedAdminOrgAccount_DecodeErrorKeepsOtherEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeInspector2DelegatedAdminAccount,
		inspector2DelegatedAdminNativeID(testRegion, testAccountID, "888888888888"), testRegion, `not json`)
	adminID := seedI2DelegatedAdmin(t, st, testInspector2Member, i2types.DelegatedAdminAccount{AccountId: sdkaws.String(testInspector2Member)})
	orgAcctID := seedI2OrgAccount(t, st, testInspector2Member)

	err := resolveInspector2DelegatedAdminOrgAccount(acct, st)
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("err = %v; want the attributes decode error", err)
	}
	assertRelationship(t, kmsRelsFrom(t, st, adminID), adminID, orgAcctID, store.RelAttachedTo)
}
