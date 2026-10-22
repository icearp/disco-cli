package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/braket"
	braketTypes "github.com/aws/aws-sdk-go-v2/service/braket/types"
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
	untagged := fmt.Sprintf("arn:aws:braket:%s:%s:spending-limit/sl-2", testRegion, acct.ID)
	device := "arn:aws:braket:::device/qpu/ionq/Aria-1"
	queued := "0"
	limit := "100"
	now := time.Unix(1700000000, 0).UTC()
	stub := &stubBraket{
		limits: []braketTypes.SpendingLimitSummary{
			{SpendingLimitArn: &arn, DeviceArn: &device, QueuedSpend: &queued, SpendingLimit: &limit, CreatedAt: &now, Tags: map[string]string{"team": "quantum"}},
			{SpendingLimitArn: &untagged, DeviceArn: &device},
		},
	}
	total, inserted, err := scanBraketSpendingLimits(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("total=%d inserted=%d want 2/2", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeBraketSpendingLimit)
	for _, a := range []string{arn, untagged} {
		if _, ok := rows[a]; !ok {
			t.Fatalf("spending limit %s missing", a)
		}
	}
	assertTagsMap(t, rows[arn].TagsJSON, map[string]string{"team": "quantum"})
	if got := rows[untagged].TagsJSON; got != nil {
		t.Errorf("untagged spending limit TagsJSON = %q, want nil", *got)
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
