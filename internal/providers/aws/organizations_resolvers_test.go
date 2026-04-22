package aws

import (
	"fmt"
	"testing"
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

// TestLoadOrgTargetIndex seeds OUs + accounts with their native ids embedded
// in the attributes JSON (as the scanner does) and verifies the resolver's
// translation index resolves each id to the correct ARN and type.
func TestLoadOrgTargetIndex(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ouARN := fmt.Sprintf("arn:aws:organizations::%s:ou/o-abcd/ou-root-12345678", acct.ID)
	accARN := fmt.Sprintf("arn:aws:organizations::%s:account/o-abcd/111122223333", acct.ID)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsOU, ouARN, "", `{"Id": "ou-root-12345678"}`)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeOrganizationsAccount, accARN, "", `{"Id": "111122223333"}`)

	arnByID, typeByID, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		t.Fatalf("loadOrgTargetIndex: %v", err)
	}
	if arnByID["ou-root-12345678"] != ouARN {
		t.Errorf("OU arn: got %q, want %q", arnByID["ou-root-12345678"], ouARN)
	}
	if typeByID["111122223333"] != TypeOrganizationsAccount {
		t.Errorf("account type: got %q, want %q", typeByID["111122223333"], TypeOrganizationsAccount)
	}
}
