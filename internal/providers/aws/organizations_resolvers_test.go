package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	organizationstypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

// TestExtractPolicyID covers the SCP ARN → p-xxxx id parser used by the
// resolver; shape taken from real Organizations SCP ARNs.
func TestExtractPolicyID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"arn:aws:organizations::111:policy/o-abcd/service_control_policy/p-12345678", "p-12345678"},
		{"arn:aws:organizations::111:policy/o-abcd/service_control_policy/p-x", "p-x"},
	}
	for _, tc := range cases {
		got := extractPolicyID(tc.in)
		if got == nil || *got != tc.want {
			t.Errorf("extractPolicyID(%q): got %v, want %q", tc.in, got, tc.want)
		}
	}
	if extractPolicyID("malformed") != nil {
		t.Error("malformed ARN should return nil")
	}
	if extractPolicyID("arn:trailing/") != nil {
		t.Error("trailing slash should return nil")
	}
}

// TestLoadOrgTargetIndex seeds a root, an OU and an account with their native ids embedded
// in the attributes JSON (as the scanner does) and verifies the resolver's
// translation index resolves each id to the correct ARN and type.
func TestLoadOrgTargetIndex(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ouARN := fmt.Sprintf("arn:aws:organizations::%s:ou/o-abcd/ou-root-12345678", acct.ID)
	accARN := fmt.Sprintf("arn:aws:organizations::%s:account/o-abcd/111122223333", acct.ID)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsOU, ouARN, "", `{"Id": "ou-root-12345678"}`)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsAccount, accARN, "", `{"Id": "111122223333"}`)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsRoot, orgTestRootARN, "",
		mustJSON(organizationstypes.Root{Id: sdkaws.String("r-ab12"), Arn: sdkaws.String(orgTestRootARN)}))

	arnByID, typeByID, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		t.Fatalf("loadOrgTargetIndex: %v", err)
	}
	if arnByID["ou-root-12345678"] != ouARN {
		t.Errorf("OU arn: got %q, want %q", arnByID["ou-root-12345678"], ouARN)
	}
	if arnByID["r-ab12"] != orgTestRootARN || typeByID["r-ab12"] != TypeOrganizationsRoot {
		t.Errorf("root: got %q/%q, want %q/%q", arnByID["r-ab12"], typeByID["r-ab12"], orgTestRootARN, TypeOrganizationsRoot)
	}
	if typeByID["111122223333"] != TypeOrganizationsAccount {
		t.Errorf("account type: got %q, want %q", typeByID["111122223333"], TypeOrganizationsAccount)
	}
}

const orgTestSCPARN = "arn:aws:organizations::" + testAccountID + ":policy/o-abc/service_control_policy/p-abc12345"

// scpTargetsAcct answers ListTargetsForPolicy with the root, the OU, account
// 111111111111 and a root that is not in the store.
func scpTargetsAcct() *account {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"ListTargetsForPolicy": func(any) (any, error) {
			return &organizations.ListTargetsForPolicyOutput{Targets: []organizationstypes.PolicyTargetSummary{
				{TargetId: sdkaws.String("r-ab12"), Arn: sdkaws.String(orgTestRootARN), Type: organizationstypes.TargetTypeRoot},
				{TargetId: sdkaws.String("ou-ab12-11111111"), Arn: sdkaws.String(orgTestOUARN), Type: organizationstypes.TargetTypeOrganizationalUnit},
				{TargetId: sdkaws.String("111111111111"), Arn: sdkaws.String(orgTestAcct1ARN), Type: organizationstypes.TargetTypeAccount},
				{TargetId: sdkaws.String("r-ghost"), Type: organizationstypes.TargetTypeRoot},
			}}, nil
		},
	}, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})
	return acct
}

func TestResolveOrganizationsSCPTargets_AttachesRootOUAndAccount(t *testing.T) {
	st := newTestStore(t)
	scpID := upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsSCP, orgTestSCPARN, "", "{}")
	rootID := upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsRoot, orgTestRootARN, "",
		mustJSON(organizationstypes.Root{Id: sdkaws.String("r-ab12"), Arn: sdkaws.String(orgTestRootARN)}))
	ouID := upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsOU, orgTestOUARN, "",
		mustJSON(organizationstypes.OrganizationalUnit{Id: sdkaws.String("ou-ab12-11111111"), Arn: sdkaws.String(orgTestOUARN)}))
	acctID := upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsAccount, orgTestAcct1ARN, "",
		mustJSON(organizationstypes.Account{Id: sdkaws.String("111111111111"), Arn: sdkaws.String(orgTestAcct1ARN)}))

	if err := resolveOrganizationsSCPTargets(scpTargetsAcct(), st); err != nil {
		t.Fatalf("resolveOrganizationsSCPTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(scpID, store.RelAttachedTo)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 3 {
		t.Errorf("SCP edges = %d, want 3 (root, OU, account; the unstored root gets none)", len(rels))
	}
	assertRelationship(t, rels, scpID, rootID, store.RelAttachedTo)
	assertRelationship(t, rels, scpID, ouID, store.RelAttachedTo)
	assertRelationship(t, rels, scpID, acctID, store.RelAttachedTo)
}

// TestResolveOrganizationsSCPTargets_RootWithoutAttrs pins that a root row
// whose attributes carry no Id is left out of the index: no edge, no error.
func TestResolveOrganizationsSCPTargets_RootWithoutAttrs(t *testing.T) {
	st := newTestStore(t)
	scpID := upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsSCP, orgTestSCPARN, "", "{}")
	_ = upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsRoot, orgTestRootARN, "", "{}")

	if err := resolveOrganizationsSCPTargets(scpTargetsAcct(), st); err != nil {
		t.Fatalf("resolveOrganizationsSCPTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(scpID, store.RelAttachedTo)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("SCP edges = %v, want none", rels)
	}
}

// A denied ListTargetsForPolicy leaves the SCP without edges, and the denial
// is a scan warning rather than an SCP that silently attaches to nothing.
func TestResolveOrganizationsSCPTargets_DeniedTargetsWarn(t *testing.T) {
	st := newTestStore(t)
	scpID := upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsSCP, orgTestSCPARN, "", "{}")
	warnings := countOpWarnings(st, "organizations:ListTargetsForPolicy")
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(nil, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})

	if err := resolveOrganizationsSCPTargets(acct, st); err != nil {
		t.Fatalf("resolveOrganizationsSCPTargets: %v", err)
	}
	if *warnings != 1 {
		t.Errorf("ListTargetsForPolicy warnings = %d, want 1", *warnings)
	}
	if rels, err := st.RelationshipsFrom(scpID); err != nil || len(rels) != 0 {
		t.Errorf("RelationshipsFrom(scp) = %v, %v; want none", rels, err)
	}
}

// Denials on some SCPs and some admin accounts produce one warning per op,
// however many items were denied, and the items that were not denied still
// get their edges.
func TestResolveOrganizations_PerItemDenialsShareOneWarning(t *testing.T) {
	deny := &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"}
	scpARN := func(id string) string {
		return "arn:aws:organizations::" + testAccountID + ":policy/o-abc/service_control_policy/" + id
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(in any) (any, error){
		"ListTargetsForPolicy": func(in any) (any, error) {
			if id := sdkaws.ToString(in.(*organizations.ListTargetsForPolicyInput).PolicyId); id != "p-allowed" {
				return nil, deny
			}
			return &organizations.ListTargetsForPolicyOutput{Targets: []organizationstypes.PolicyTargetSummary{
				{TargetId: sdkaws.String("111111111111"), Type: organizationstypes.TargetTypeAccount},
			}}, nil
		},
		"ListDelegatedAdministrators": func(any) (any, error) {
			return &organizations.ListDelegatedAdministratorsOutput{DelegatedAdministrators: []organizationstypes.DelegatedAdministrator{
				{Id: sdkaws.String("111111111111")}, {Id: sdkaws.String("222222222222")}, {Id: sdkaws.String("333333333333")},
			}}, nil
		},
		"ListDelegatedServicesForAccount": func(in any) (any, error) {
			if sdkaws.ToString(in.(*organizations.ListDelegatedServicesForAccountInput).AccountId) != "111111111111" {
				return nil, deny
			}
			return &organizations.ListDelegatedServicesForAccountOutput{DelegatedServices: []organizationstypes.DelegatedService{
				{ServicePrincipal: sdkaws.String("guardduty.amazonaws.com")},
			}}, nil
		},
	}, deny)

	st, orgID, adminID := seedDelegatedAdminStore(t)
	acct3ID := upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsAccount, orgTestAcct3ARN, "",
		mustJSON(organizationstypes.Account{Id: sdkaws.String("333333333333"), Arn: sdkaws.String(orgTestAcct3ARN)}))
	allowedSCP := upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsSCP, scpARN("p-allowed"), "", "{}")
	for _, id := range []string{"p-deny1", "p-deny2"} {
		upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsSCP, scpARN(id), "", "{}")
	}
	scpWarnings := countOpWarnings(st, "organizations:ListTargetsForPolicy")
	if err := resolveOrganizationsSCPTargets(acct, st); err != nil {
		t.Fatalf("resolveOrganizationsSCPTargets: %v", err)
	}
	if *scpWarnings != 1 {
		t.Errorf("ListTargetsForPolicy warnings = %d, want 1 for 2 denied SCPs", *scpWarnings)
	}
	rels, err := st.RelationshipsFrom(allowedSCP, store.RelAttachedTo)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, allowedSCP, adminID, store.RelAttachedTo)

	svcWarnings := countOpWarnings(st, "organizations:ListDelegatedServicesForAccount")
	if err := resolveOrganizationsDelegatedAdmins(acct, st); err != nil {
		t.Fatalf("resolveOrganizationsDelegatedAdmins: %v", err)
	}
	if *svcWarnings != 1 {
		t.Errorf("ListDelegatedServicesForAccount warnings = %d, want 1 for 2 denied admins", *svcWarnings)
	}
	if attrs := delegatedAdminEdgeAttrs(t, st, orgID, adminID); attrs == nil {
		t.Error("allowed admin edge attributes = nil; want its DelegatedServices")
	}
	if attrs := delegatedAdminEdgeAttrs(t, st, orgID, acct3ID); attrs != nil {
		t.Errorf("denied admin edge attributes = %s; want none", *attrs)
	}
}

// delegatedAdminAcct answers ListDelegatedAdministrators with account
// 111111111111 and ListDelegatedServicesForAccount with services, or denies
// the latter when services is nil.
func delegatedAdminAcct(services []string) *account {
	handlers := map[string]func(in any) (any, error){
		"ListDelegatedAdministrators": func(any) (any, error) {
			return &organizations.ListDelegatedAdministratorsOutput{DelegatedAdministrators: []organizationstypes.DelegatedAdministrator{
				{Id: sdkaws.String("111111111111"), Arn: sdkaws.String(orgTestAcct1ARN)},
			}}, nil
		},
	}
	if services != nil {
		handlers["ListDelegatedServicesForAccount"] = func(any) (any, error) {
			out := &organizations.ListDelegatedServicesForAccountOutput{}
			for _, s := range services {
				out.DelegatedServices = append(out.DelegatedServices, organizationstypes.DelegatedService{ServicePrincipal: sdkaws.String(s)})
			}
			return out, nil
		}
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})
	return acct
}

func seedDelegatedAdminStore(t *testing.T) (st *store.Store, orgID, adminID string) {
	t.Helper()
	st = newTestStore(t)
	orgID = upsertTestResource(t, st, "aws", testAccountID, TypeOrganization, orgTestOrgARN, "", "{}")
	adminID = upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsAccount, orgTestAcct1ARN, "",
		mustJSON(organizationstypes.Account{Id: sdkaws.String("111111111111"), Arn: sdkaws.String(orgTestAcct1ARN)}))
	return st, orgID, adminID
}

// delegatedAdminEdgeAttrs returns the attributes of the org→admin edge, failing
// the test when the edge is missing.
func delegatedAdminEdgeAttrs(t *testing.T, st *store.Store, orgID, adminID string) *string {
	t.Helper()
	rels, err := st.RelationshipsFrom(orgID, store.RelAttachedTo)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, orgID, adminID, store.RelAttachedTo)
	for _, r := range rels {
		if r.ToID == adminID {
			return r.Attributes
		}
	}
	return nil
}

func TestResolveOrganizationsDelegatedAdmins_ServicesOnEdge(t *testing.T) {
	st, orgID, adminID := seedDelegatedAdminStore(t)

	if err := resolveOrganizationsDelegatedAdmins(delegatedAdminAcct([]string{"guardduty.amazonaws.com"}), st); err != nil {
		t.Fatalf("resolveOrganizationsDelegatedAdmins: %v", err)
	}
	attrs := delegatedAdminEdgeAttrs(t, st, orgID, adminID)
	if attrs == nil {
		t.Fatal("edge attributes = nil; want DelegatedServices")
	}
	var got struct{ DelegatedServices []string }
	if err := json.Unmarshal([]byte(*attrs), &got); err != nil {
		t.Fatalf("decode edge attributes %s: %v", *attrs, err)
	}
	if len(got.DelegatedServices) != 1 || got.DelegatedServices[0] != "guardduty.amazonaws.com" {
		t.Errorf("DelegatedServices = %v; want [guardduty.amazonaws.com]", got.DelegatedServices)
	}
}

// A denied ListDelegatedServicesForAccount keeps the edge, which
// ListDelegatedAdministrators proved, but records no DelegatedServices list:
// an empty list would claim the account administers no service.
func TestResolveOrganizationsDelegatedAdmins_DeniedServicesKeepEdgeWithoutList(t *testing.T) {
	st, orgID, adminID := seedDelegatedAdminStore(t)
	warnings := countOpWarnings(st, "organizations:ListDelegatedServicesForAccount")

	if err := resolveOrganizationsDelegatedAdmins(delegatedAdminAcct(nil), st); err != nil {
		t.Fatalf("resolveOrganizationsDelegatedAdmins: %v", err)
	}
	if attrs := delegatedAdminEdgeAttrs(t, st, orgID, adminID); attrs != nil {
		t.Errorf("edge attributes = %s; want none after a denied services read", *attrs)
	}
	if *warnings != 1 {
		t.Errorf("ListDelegatedServicesForAccount warnings = %d, want 1", *warnings)
	}
}

func TestResolveOrganizationsDelegatedAdmins_DeniedAdminsWarn(t *testing.T) {
	st, orgID, _ := seedDelegatedAdminStore(t)
	warnings := countOpWarnings(st, "organizations:ListDelegatedAdministrators")
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(nil, &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "not authorized"})

	if err := resolveOrganizationsDelegatedAdmins(acct, st); err != nil {
		t.Fatalf("resolveOrganizationsDelegatedAdmins: %v", err)
	}
	if *warnings != 1 {
		t.Errorf("ListDelegatedAdministrators warnings = %d, want 1", *warnings)
	}
	if rels, err := st.RelationshipsFrom(orgID); err != nil || len(rels) != 0 {
		t.Errorf("RelationshipsFrom(org) = %v, %v; want none", rels, err)
	}
}

// An org row whose attributes do not parse is reported, and the org rows
// listed before and after it are still wired. Names a, b, c fix the listing
// order (ListResources orders by type, then name).
func TestResolveOrganizationsManagementAccount_BadAttrsReportedOthersWired(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	masterID := upsertTestResource(t, st, "aws", testAccountID, TypeOrganizationsAccount, orgTestAcct1ARN, "",
		mustJSON(organizationstypes.Account{Id: sdkaws.String("111111111111"), Arn: sdkaws.String(orgTestAcct1ARN)}))
	orgAttrs := mustJSON(organizationstypes.Organization{MasterAccountId: sdkaws.String("111111111111")})
	before := upsertTestResourceNamed(t, st, TypeOrganization, orgTestOrgARN, "", orgAttrs, "a")
	upsertTestResourceNamed(t, st, TypeOrganization, orgTestOrgARN+"-bad", "", `{"MasterAccountId":7}`, "b")
	after := upsertTestResourceNamed(t, st, TypeOrganization, orgTestOrgARN+"-c", "", orgAttrs, "c")

	var typeErr *json.UnmarshalTypeError
	if err := resolveOrganizationsManagementAccount(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
	for _, id := range []string{before, after} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		assertRelationship(t, rels, id, masterID, store.RelAttachedTo)
	}
}
