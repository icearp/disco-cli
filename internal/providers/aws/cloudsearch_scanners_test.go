package aws

import (
	"context"
	"errors"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/cloudsearch"
	smithy "github.com/aws/smithy-go"
)

// stubCloudSearch returns a fixed error from ListDomainNames.
type stubCloudSearch struct{ listErr error }

func (s *stubCloudSearch) ListDomainNames(_ context.Context, _ *cloudsearch.ListDomainNamesInput, _ ...func(*cloudsearch.Options)) (*cloudsearch.ListDomainNamesOutput, error) {
	return nil, s.listErr
}

func (s *stubCloudSearch) DescribeDomains(_ context.Context, _ *cloudsearch.DescribeDomainsInput, _ ...func(*cloudsearch.Options)) (*cloudsearch.DescribeDomainsOutput, error) {
	return &cloudsearch.DescribeDomainsOutput{}, nil
}

// An account AWS hasn't made eligible for CloudSearch gets NotAuthorized with a
// "not supported on this account" body. It must surface as the not-entitled
// sentinel (progress line "(account: not entitled)") and record NO
// warning — the account can't self-enable it.
func TestScanCloudSearch_NotEntitled(t *testing.T) {
	st := newTestStore(t)
	var warnings int
	st.OnWarn = func(store.ScanWarning) { warnings++ }
	acct := &account{ID: testAccountID, Name: "test"}
	stub := &stubCloudSearch{listErr: &smithy.GenericAPIError{
		Code:    "NotAuthorized",
		Message: "New domain creation not supported on this account. Please reach out to AWS Support for assistance.",
	}}

	_, _, err := scanCloudSearchWithClient(context.Background(), stub, acct, testRegion, st, testScanID)
	if !errors.Is(err, errServiceNotEntitled) {
		t.Fatalf("err = %v; want errServiceNotEntitled", err)
	}
	if warnings != 0 {
		t.Errorf("recorded %d warnings; want 0 (not-entitled must not warn)", warnings)
	}
}
