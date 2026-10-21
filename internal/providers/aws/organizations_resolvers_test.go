package aws

import (
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
