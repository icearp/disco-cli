package aws

import (
	"context"
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/billing"
	billingtypes "github.com/aws/aws-sdk-go-v2/service/billing/types"
)

type stubBilling struct {
	views []billingtypes.BillingViewListElement
}

func (s *stubBilling) ListBillingViews(_ context.Context, _ *billing.ListBillingViewsInput, _ ...func(*billing.Options)) (*billing.ListBillingViewsOutput, error) {
	return &billing.ListBillingViewsOutput{BillingViews: s.views}, nil
}

func TestScanBillingViews(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := fmt.Sprintf("arn:aws:billing::%s:billingview/primary", acct.ID)
	name := "primary"
	owner := acct.ID
	stub := &stubBilling{
		views: []billingtypes.BillingViewListElement{
			{Arn: &arn, Name: &name, OwnerAccountId: &owner, BillingViewType: billingtypes.BillingViewTypePrimary},
		},
	}
	total, inserted, err := scanBillingViews(context.Background(), stub, acct, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("total=%d inserted=%d want 1/1", total, inserted)
	}
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBillingView, arn)); err != nil {
		t.Errorf("billing view missing: %v", err)
	}
}

func TestScanBillingViewsEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubBilling{}
	total, inserted, err := scanBillingViews(context.Background(), stub, acct, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}

func TestScanBillingViewsSkipsBlankARN(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	blank := ""
	name := "incomplete"
	stub := &stubBilling{views: []billingtypes.BillingViewListElement{{Arn: &blank, Name: &name}}}
	total, _, err := scanBillingViews(context.Background(), stub, acct, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Fatalf("total=%d want 0 (blank-arn entry skipped)", total)
	}
}
