package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/timestreamwrite"

	"codeberg.org/icearp/disco/store"
)

// stubTSWrite returns errList on ListDatabases; only that method is exercised.
type stubTSWrite struct {
	tsWriteAPI
	errList error
}

func (s *stubTSWrite) ListDatabases(context.Context, *timestreamwrite.ListDatabasesInput, ...func(*timestreamwrite.Options)) (*timestreamwrite.ListDatabasesOutput, error) {
	return nil, s.errList
}

// TestScanTSDatabases_ClosedToNewCustomers pins that the closed-to-new-customers
// AccessDeniedException maps to the not-entitled sentinel (not disabled) and
// records no warning — Timestream for LiveAnalytics can't be self-enabled.
func TestScanTSDatabases_ClosedToNewCustomers(t *testing.T) {
	st := newTestStore(t)
	var warned bool
	st.OnWarn = func(store.ScanWarning) { warned = true }
	acct := newTestAccount(testAccountID)

	closed := apiErr("AccessDeniedException",
		"Only existing Timestream for LiveAnalytics customers can access the service")
	stub := &stubTSWrite{errList: closed}

	_, _, err := scanTSDatabases(context.Background(), stub, acct, testRegion, st, testScanID)
	if !errors.Is(err, errServiceNotEntitled) {
		t.Fatalf("got %v; want errServiceNotEntitled", err)
	}
	if errors.Is(err, errServiceDisabled) {
		t.Errorf("got %v; must not also be errServiceDisabled", err)
	}
	if warned {
		t.Errorf("closed-to-new-customers should not record a ScanWarning")
	}
}

// TestScanTSDatabases_RealIAMDeny is the negative case: a genuine per-action IAM
// denial carries an action-identifying message, so it must NOT be classified as
// not-entitled — it soft-skips (nil) and records a warning instead.
func TestScanTSDatabases_RealIAMDeny(t *testing.T) {
	st := newTestStore(t)
	var warned bool
	st.OnWarn = func(store.ScanWarning) { warned = true }
	acct := newTestAccount(testAccountID)

	deny := apiErr("AccessDeniedException",
		"User: arn:aws:iam::123456789012:user/x is not authorized to perform: timestream:ListDatabases")
	stub := &stubTSWrite{errList: deny}

	_, _, err := scanTSDatabases(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("got %v; want nil (soft-skip)", err)
	}
	if !warned {
		t.Errorf("real IAM denial should record a ScanWarning")
	}
}
