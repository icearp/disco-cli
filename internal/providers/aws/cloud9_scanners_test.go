package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/icearp/disco-cli/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	smithymw "github.com/aws/smithy-go/middleware"
)

func cloud9CfgWithStub(stub func(*smithymw.Stack) error, region string) sdkaws.Config {
	return sdkaws.Config{
		Region:           region,
		Credentials:      credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
		RetryMaxAttempts: 1,
		APIOptions:       []func(*smithymw.Stack) error{stub},
	}
}

// TestScanCloud9_ClosedToAccountExplicitMessage verifies the non-empty
// "does not have access to the Cloud9 service" AccessDeniedException (AWS's
// closed-to-new-customers signal for never-onboarded accounts) maps to the
// not-entitled sentinel — Cloud9 can't be self-enabled — with no warning.
func TestScanCloud9_ClosedToAccountExplicitMessage(t *testing.T) {
	st := newTestStore(t)
	warnings := 0
	st.OnWarn = func(_ store.ScanWarning) { warnings++ }

	stub := stubResponses(t, map[string][]stubCall{
		"ListEnvironments": {{Err: apiErr("AccessDeniedException",
			"This account does not have access to the Cloud9 service")}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

	_, _, err := scanCloud9(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, errServiceNotEntitled) {
		t.Fatalf("scanCloud9: got %v; want errServiceNotEntitled", err)
	}
	if warnings != 0 {
		t.Errorf("warnings=%d, want 0 (closed-state must not warn)", warnings)
	}
}

// TestScanCloud9_RealIAMDenialStillWarns confirms the message-disambiguated
// silent-skip doesn't swallow a genuine per-op IAM denial — those carry an
// action-identifying message and must still warn.
func TestScanCloud9_RealIAMDenialStillWarns(t *testing.T) {
	st := newTestStore(t)
	warnings := 0
	st.OnWarn = func(_ store.ScanWarning) { warnings++ }

	stub := stubResponses(t, map[string][]stubCall{
		"ListEnvironments": {{Err: apiErr("AccessDeniedException",
			"User: arn:aws:iam::123456789012:user/x is not authorized to perform: cloud9:ListEnvironments")}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

	total, inserted, err := scanCloud9(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloud9: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0", total, inserted)
	}
	if warnings != 1 {
		t.Errorf("warnings=%d, want 1 (real IAM denial must warn)", warnings)
	}
}
