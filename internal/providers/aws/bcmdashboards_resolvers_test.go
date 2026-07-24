package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
	bcmdtypes "github.com/aws/aws-sdk-go-v2/service/bcmdashboards/types"
)

func bcmDashScheduledReportAttrs(t *testing.T, r bcmdtypes.ScheduledReportSummary) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal scheduled-report: %v", err)
	}
	return string(b)
}

func TestResolveBCMDashboardsScheduledReports(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dashARN := fmt.Sprintf("arn:aws:bcm-dashboards::%s:dashboard/dash-1111", testAccountID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeBCMDashboardsDashboard, dashARN, "", "{}")

	repARN := fmt.Sprintf("arn:aws:bcm-dashboards::%s:scheduled-report/rep-2222", testAccountID)
	repName := "monthly"
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeBCMDashboardsScheduledReport, repARN, "",
		bcmDashScheduledReportAttrs(t, bcmdtypes.ScheduledReportSummary{Arn: &repARN, Name: &repName, DashboardArn: &dashARN}))

	if err := resolveBCMDashboardsScheduledReports(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rels, rID, dID, store.RelAttachedTo)
}

// A scheduled report pointing at an unscanned dashboard, and one with empty
// attrs (no DashboardArn), both emit no edge and don't panic.
func TestResolveBCMDashboardsScheduledReports_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	repARN := fmt.Sprintf("arn:aws:bcm-dashboards::%s:scheduled-report/rep-orphan", testAccountID)
	repName := "orphan"
	goneDash := fmt.Sprintf("arn:aws:bcm-dashboards::%s:dashboard/dash-gone", testAccountID)
	orphanID := upsertTestResource(t, st, "aws", acct.ID, TypeBCMDashboardsScheduledReport, repARN, "",
		bcmDashScheduledReportAttrs(t, bcmdtypes.ScheduledReportSummary{Arn: &repARN, Name: &repName, DashboardArn: &goneDash}))

	emptyARN := fmt.Sprintf("arn:aws:bcm-dashboards::%s:scheduled-report/rep-empty", testAccountID)
	emptyID := upsertTestResource(t, st, "aws", acct.ID, TypeBCMDashboardsScheduledReport, emptyARN, "", "{}")

	if err := resolveBCMDashboardsScheduledReports(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, id := range []string{orphanID, emptyID} {
		rels, _ := st.RelationshipsFrom(id)
		if len(rels) != 0 {
			t.Errorf("report %s emitted %d edges, want 0", id, len(rels))
		}
	}
}
