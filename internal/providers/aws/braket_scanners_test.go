package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/braket"
	braketTypes "github.com/aws/aws-sdk-go-v2/service/braket/types"
	"github.com/icearp/disco-cli/store"
)

type stubBraket struct {
	limits []braketTypes.SpendingLimitSummary
}

func (s *stubBraket) SearchSpendingLimits(_ context.Context, _ *braket.SearchSpendingLimitsInput, _ ...func(*braket.Options)) (*braket.SearchSpendingLimitsOutput, error) {
	return &braket.SearchSpendingLimitsOutput{SpendingLimits: s.limits}, nil
}

func TestScanBraketSpendingLimits(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := fmt.Sprintf("arn:aws:braket:%s:%s:spending-limit/sl-1", testRegion, acct.ID)
	device := "arn:aws:braket:::device/qpu/ionq/Aria-1"
	queued := "0"
	limit := "100"
	now := time.Unix(1700000000, 0).UTC()
	stub := &stubBraket{
		limits: []braketTypes.SpendingLimitSummary{
			{SpendingLimitArn: &arn, DeviceArn: &device, QueuedSpend: &queued, SpendingLimit: &limit, CreatedAt: &now},
		},
	}
	total, inserted, err := scanBraketSpendingLimits(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("total=%d inserted=%d want 1/1", total, inserted)
	}
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, arn)); err != nil {
		t.Errorf("spending limit missing: %v", err)
	}
}

func TestScanBraketSpendingLimitsEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubBraket{}
	total, inserted, err := scanBraketSpendingLimits(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
