package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"

	"github.com/icearp/disco-cli/store"
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
	sumErr   error
	aliasErr error
}

// runIAMAccount runs scanIAMAccount under scanID and joins the errors it
// reported the way scanIAM does.
func runIAMAccount(st *store.Store, client iamAPI, scanID string) (int, error) {
	acct := newTestAccount(testAccountID)
	var errs phaseErrs
	total, _ := scanIAMAccount(context.Background(), client, acct, st, scanID, errs.add)
	return total, errs.join(st, acct, "global")
}

func (s stubIAMAccountAPI) GetAccountSummary(context.Context, *iam.GetAccountSummaryInput, ...func(*iam.Options)) (*iam.GetAccountSummaryOutput, error) {
	if s.sumErr != nil {
		return nil, s.sumErr
	}
	return &iam.GetAccountSummaryOutput{SummaryMap: s.summary}, nil
}

func (s stubIAMAccountAPI) ListAccountAliases(context.Context, *iam.ListAccountAliasesInput, ...func(*iam.Options)) (*iam.ListAccountAliasesOutput, error) {
	if s.aliasErr != nil {
		return nil, s.aliasErr
	}
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
	minLen := int32(14)
	client := stubIAMAccountAPI{
		summary:  map[string]int32{"Users": 3},
		aliases:  []string{"acme-prod"},
		pwPolicy: &iamtypes.PasswordPolicy{MinimumPasswordLength: &minLen},
	}

	if _, err := runIAMAccount(st, client, testScanID); err != nil {
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
	client := stubIAMAccountAPI{
		summary: map[string]int32{"Users": 0},
		pwErr:   &iamtypes.NoSuchEntityException{},
	}

	if _, err := runIAMAccount(st, client, testScanID); err != nil {
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

// iamAccountAttrs returns the stored account row's attribute keys.
func iamAccountAttrs(t *testing.T, st *store.Store) map[string]json.RawMessage {
	t.Helper()
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, iamAccountNativeID(testAccountID)))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	return attrs
}

// A read that fails other than by a denial withholds the account row this
// scan, so the row an earlier scan stored keeps the key; the failure is
// returned under its op, so the scan is partial.
func TestScanIAMAccount_FailedReadWithholdsRow(t *testing.T) {
	throttled := &smithy.GenericAPIError{Code: "Throttling", Message: "Rate exceeded"}
	full := stubIAMAccountAPI{
		summary:  map[string]int32{"Users": 1},
		aliases:  []string{"prod"},
		pwPolicy: &iamtypes.PasswordPolicy{},
	}
	for _, tc := range []struct {
		name, op, key string
		fail          func(*stubIAMAccountAPI)
	}{
		{"summary throttled", "iam:GetAccountSummary", "SummaryMap", func(c *stubIAMAccountAPI) { c.sumErr = throttled }},
		{"alias throttled", "iam:ListAccountAliases", "AccountAliases", func(c *stubIAMAccountAPI) { c.aliasErr = throttled }},
		{"password policy throttled", "iam:GetAccountPasswordPolicy", "PasswordPolicy", func(c *stubIAMAccountAPI) { c.pwErr = throttled }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			insertTestScan(t, st, "scan-earlier", "completed")
			if _, err := runIAMAccount(st, full, "scan-earlier"); err != nil {
				t.Fatalf("earlier scan: %v", err)
			}
			failing := full
			tc.fail(&failing)
			warns := countOpWarnings(st, tc.op)
			total, err := runIAMAccount(st, failing, testScanID)
			if got := classifyServiceError(err); got != outcomeTransient {
				t.Errorf("classifyServiceError(%v) = %v; want outcomeTransient", err, got)
			}
			if *warns != 0 {
				t.Errorf("%s warnings = %d; want 0: only a denial warns", tc.op, *warns)
			}
			if total != 0 {
				t.Errorf("total = %d; want 0: no row written this scan", total)
			}
			if n := countStoredRows(t, st); n != 1 {
				t.Errorf("stored rows = %d; want only the earlier scan's row", n)
			}
			if _, ok := iamAccountAttrs(t, st)[tc.key]; !ok {
				t.Errorf("latest row lost %s; want the earlier scan's row unchanged", tc.key)
			}
		})
	}
}

// A denied read keeps the row without the denied key and warns once; the scan
// is not failed.
func TestScanIAMAccount_DeniedReadKeepsRow(t *testing.T) {
	denied := &smithy.GenericAPIError{Code: "AccessDenied", Message: "is not authorized to perform: iam:GetAccountPasswordPolicy"}
	st := newTestStore(t)
	warns := countOpWarnings(st, "iam:GetAccountPasswordPolicy")
	total, err := runIAMAccount(st, stubIAMAccountAPI{summary: map[string]int32{"Users": 1}, pwErr: denied}, testScanID)
	if err != nil {
		t.Errorf("err = %v; want nil for a denial", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1", *warns)
	}
	if total != 1 {
		t.Fatalf("total = %d; want the account row stored", total)
	}
	attrs := iamAccountAttrs(t, st)
	if _, ok := attrs["PasswordPolicy"]; ok {
		t.Errorf("attrs = %v; want PasswordPolicy absent", attrs)
	}
	if _, ok := attrs["SummaryMap"]; !ok {
		t.Errorf("attrs = %v; want SummaryMap kept", attrs)
	}
}

// Beside another phase's hard error, each failed account read is demoted to a
// warning under its own op, not under the scanner's first op.
func TestScanIAMAccount_ErrorsLabelledByOwnOp(t *testing.T) {
	throttled := &smithy.GenericAPIError{Code: "Throttling", Message: "Rate exceeded"}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	warned := recordWarnings(st)
	var errs phaseErrs
	scanIAMAccount(context.Background(), stubIAMAccountAPI{aliasErr: throttled, pwErr: throttled}, acct, st, testScanID, errs.add)
	errs.add("iam:ListInstanceProfiles", errors.New("hard"))
	_ = errs.join(st, acct, "global")
	got := map[string]int{}
	for _, w := range *warned {
		got[w.Service]++
	}
	want := map[string]int{"iam:ListAccountAliases": 1, "iam:GetAccountPasswordPolicy": 1}
	if !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
}
