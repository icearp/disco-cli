package aws

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"codeberg.org/icearp/disco/store"
)

// stubIAMAccountAPI implements only the three account-posture methods
// scanIAMAccount calls; embedded iamAPI leaves the rest unimplemented (never
// invoked by this scanner, would panic if they were).
type stubIAMAccountAPI struct {
	iamAPI
	summary  map[string]int32
	aliases  []string
	pwPolicy *iamtypes.PasswordPolicy
	pwErr    error
}

func (s stubIAMAccountAPI) GetAccountSummary(context.Context, *iam.GetAccountSummaryInput, ...func(*iam.Options)) (*iam.GetAccountSummaryOutput, error) {
	return &iam.GetAccountSummaryOutput{SummaryMap: s.summary}, nil
}

func (s stubIAMAccountAPI) ListAccountAliases(context.Context, *iam.ListAccountAliasesInput, ...func(*iam.Options)) (*iam.ListAccountAliasesOutput, error) {
	return &iam.ListAccountAliasesOutput{AccountAliases: s.aliases}, nil
}

func (s stubIAMAccountAPI) GetAccountPasswordPolicy(context.Context, *iam.GetAccountPasswordPolicyInput, ...func(*iam.Options)) (*iam.GetAccountPasswordPolicyOutput, error) {
	if s.pwErr != nil {
		return nil, s.pwErr
	}
	return &iam.GetAccountPasswordPolicyOutput{PasswordPolicy: s.pwPolicy}, nil
}

// TestScanIAMAccount_PopulatesSelfNode verifies the account self-node lands at
// the canonical natural key with summary/alias/password-policy attrs, and that
// the alias becomes the row name.
func TestScanIAMAccount_PopulatesSelfNode(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	minLen := int32(14)
	client := stubIAMAccountAPI{
		summary:  map[string]int32{"Users": 3},
		aliases:  []string{"acme-prod"},
		pwPolicy: &iamtypes.PasswordPolicy{MinimumPasswordLength: &minLen},
	}

	if _, _, err := scanIAMAccount(context.Background(), client, acct, st, testScanID); err != nil {
		t.Fatalf("scanIAMAccount: %v", err)
	}

	wantID := store.ResourceID("aws", testAccountID, "arn:aws:iam::"+testAccountID+":root")
	r, err := st.GetResource(wantID)
	if err != nil {
		t.Fatalf("GetResource self-node: %v", err)
	}
	if r.Name == nil || *r.Name != "acme-prod" {
		t.Errorf("name = %v, want alias acme-prod", r.Name)
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs unmarshal: %v", err)
	}
	for _, k := range []string{"SummaryMap", "AccountAliases", "PasswordPolicy"} {
		if _, ok := attrs[k]; !ok {
			t.Errorf("attrs missing %q: %s", k, r.AttributesJSON)
		}
	}
}

// TestScanIAMAccount_NoPasswordPolicy verifies an account with no password
// policy (NoSuchEntity) still upserts a self-node — without a PasswordPolicy
// attribute — and falls back to the account ID as name when no alias exists.
func TestScanIAMAccount_NoPasswordPolicy(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	client := stubIAMAccountAPI{
		summary: map[string]int32{"Users": 0},
		pwErr:   &iamtypes.NoSuchEntityException{},
	}

	if _, _, err := scanIAMAccount(context.Background(), client, acct, st, testScanID); err != nil {
		t.Fatalf("scanIAMAccount: %v", err)
	}

	wantID := store.ResourceID("aws", testAccountID, "arn:aws:iam::"+testAccountID+":root")
	r, err := st.GetResource(wantID)
	if err != nil {
		t.Fatalf("GetResource self-node: %v", err)
	}
	if r.Name == nil || *r.Name != testAccountID {
		t.Errorf("name = %v, want account ID fallback %q", r.Name, testAccountID)
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs unmarshal: %v", err)
	}
	if _, ok := attrs["PasswordPolicy"]; ok {
		t.Errorf("PasswordPolicy must be absent when none configured: %s", r.AttributesJSON)
	}
}
