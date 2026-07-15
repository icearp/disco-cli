package aws

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/bcmdashboards"
	bcmdtypes "github.com/aws/aws-sdk-go-v2/service/bcmdashboards/types"
)

type stubBCMDashboards struct {
	dashboards []bcmdtypes.DashboardReference
	reports    []bcmdtypes.ScheduledReportSummary
}

func (s *stubBCMDashboards) ListDashboards(_ context.Context, _ *bcmdashboards.ListDashboardsInput, _ ...func(*bcmdashboards.Options)) (*bcmdashboards.ListDashboardsOutput, error) {
	return &bcmdashboards.ListDashboardsOutput{Dashboards: s.dashboards}, nil
}

func (s *stubBCMDashboards) ListScheduledReports(_ context.Context, _ *bcmdashboards.ListScheduledReportsInput, _ ...func(*bcmdashboards.Options)) (*bcmdashboards.ListScheduledReportsOutput, error) {
	return &bcmdashboards.ListScheduledReportsOutput{ScheduledReports: s.reports}, nil
}

func TestScanBCMDashboards(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dArn := "arn:aws:bcm-dashboards::" + testAccountID + ":dashboard/dash-1"
	dName := "Spend overview"
	rArn := "arn:aws:bcm-dashboards::" + testAccountID + ":scheduled-report/rep-1"
	rName := "monthly"
	stub := &stubBCMDashboards{
		dashboards: []bcmdtypes.DashboardReference{{Arn: &dArn, Name: &dName}},
		reports:    []bcmdtypes.ScheduledReportSummary{{Arn: &rArn, Name: &rName, DashboardArn: &dArn, State: bcmdtypes.ScheduleStateEnabled}},
	}
	total, _, err := scanBCMDashboards(context.Background(), stub, acct, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Fatalf("total=%d want 2", total)
	}
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, dArn)); err != nil {
		t.Errorf("dashboard missing: %v", err)
	}
	rep, err := st.GetResource(store.ResourceID("aws", acct.ID, rArn))
	if err != nil {
		t.Fatalf("scheduled-report missing: %v", err)
	}
	// Global service → rows carry the global region marker; report Status
	// maps from r.State.
	if rep.Region == nil || *rep.Region != "global" {
		t.Errorf("scheduled-report Region=%v want \"global\"", rep.Region)
	}
	if rep.Status == nil || *rep.Status != string(bcmdtypes.ScheduleStateEnabled) {
		t.Errorf("scheduled-report Status=%v want %q", rep.Status, bcmdtypes.ScheduleStateEnabled)
	}
}

// TestScanBCMDashboards_Empty guards the empty-list path: no rows, no error.
func TestScanBCMDashboards_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	total, _, err := scanBCMDashboards(context.Background(), &stubBCMDashboards{}, acct, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 {
		t.Errorf("empty total=%d want 0", total)
	}
}
