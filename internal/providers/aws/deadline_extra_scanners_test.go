package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/deadline"
	dltypes "github.com/aws/aws-sdk-go-v2/service/deadline/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// stubDeadlineExtra embeds deadlineAPI so only the two ops the budget/volume
// scanners call need implementing.
type stubDeadlineExtra struct {
	deadlineAPI
	budgets []dltypes.BudgetSummary
	volumes []dltypes.VolumeSummary
}

func (s *stubDeadlineExtra) ListBudgets(_ context.Context, _ *deadline.ListBudgetsInput, _ ...func(*deadline.Options)) (*deadline.ListBudgetsOutput, error) {
	return &deadline.ListBudgetsOutput{Budgets: s.budgets}, nil
}

func (s *stubDeadlineExtra) ListVolumes(_ context.Context, _ *deadline.ListVolumesInput, _ ...func(*deadline.Options)) (*deadline.ListVolumesOutput, error) {
	return &deadline.ListVolumesOutput{Volumes: s.volumes}, nil
}

func TestScanDeadlineBudgetsAndVolumes(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fr := &dlFarmRef{id: "farm-1", arn: deadlineFarmARN(testRegion, acct.ID, "farm-1")}
	stub := &stubDeadlineExtra{
		budgets: []dltypes.BudgetSummary{{BudgetId: ptrStr("budget-1"), DisplayName: ptrStr("render-cap")}},
		volumes: []dltypes.VolumeSummary{{VolumeId: ptrStr("vol-1"), FleetId: ptrStr("fleet-1")}},
	}

	bt, _, err := scanDeadlineBudgets(context.Background(), stub, acct, testRegion, st, testScanID, fr)
	if err != nil || bt != 1 {
		t.Fatalf("scanDeadlineBudgets: total=%d err=%v", bt, err)
	}
	vt, _, err := scanDeadlineVolumes(context.Background(), stub, acct, testRegion, st, testScanID, fr, "fleet-1")
	if err != nil || vt != 1 {
		t.Fatalf("scanDeadlineVolumes: total=%d err=%v", vt, err)
	}
	for _, tc := range []struct{ typ, want string }{
		{TypeDeadlineBudget, fr.arn + "/budget/budget-1"},
		{TypeDeadlineVolume, fr.arn + "/volume/vol-1"},
	} {
		rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{tc.typ}, Limit: util.AllResources})
		if err != nil {
			t.Fatalf("ListResources %s: %v", tc.typ, err)
		}
		if len(rows) != 1 || rows[0].NativeID != tc.want {
			t.Errorf("%s: rows=%+v, want one with NativeID %s", tc.typ, rows, tc.want)
		}
	}
}
