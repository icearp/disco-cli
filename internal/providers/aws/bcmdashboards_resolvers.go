package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveBCMDashboardsScheduledReports,
		EdgeDecl{TypeBCMDashboardsScheduledReport, TypeBCMDashboardsDashboard, store.RelAttachedTo},
	)
}

// resolveBCMDashboardsScheduledReports wires scheduled-report → dashboard via
// the report's DashboardArn. FK-safe: a report whose dashboard wasn't scanned
// (or carries no DashboardArn) emits no edge.
func resolveBCMDashboardsScheduledReports(acct *account, st *store.Store) error {
	reports, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeBCMDashboardsScheduledReport}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(reports) == 0 {
		return nil
	}
	dashboards, err := scannedIDSet(acct, st, TypeBCMDashboardsDashboard)
	if err != nil {
		return err
	}
	for _, r := range reports {
		var attrs struct {
			DashboardArn *string `json:"DashboardArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.DashboardArn)
		if arn == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, arn)
		if !dashboards[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bcmdashboards scheduled-report→dashboard: %w", err)
		}
	}
	return nil
}
