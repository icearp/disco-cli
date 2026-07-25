package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/bcmdashboards"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// AWS BCM Dashboards is account-global — endpoints resolve only via us-east-1.
const bcmDashboardsRegion = "us-east-1"

func init() {
	registerType(restype.Descriptor{Type: TypeBCMDashboardsDashboard, Service: "bcmdashboards", Upstream: "AWS::bcm-dashboards::dashboard", Leaf: true})
	registerType(restype.Descriptor{Type: TypeBCMDashboardsScheduledReport, Service: "bcmdashboards", Upstream: "AWS::bcm-dashboards::scheduled-report"})
	registerService(serviceEntry{
		name:   "aws:bcmdashboards",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
			client := bcmdashboards.NewFromConfig(acct.cfg, func(o *bcmdashboards.Options) { o.Region = bcmDashboardsRegion })
			return scanBCMDashboards(ctx, client, acct, st, scanID)
		},
	})
}

// bcmDashboardsAPI is the narrow surface scanBCMDashboards uses.
type bcmDashboardsAPI interface {
	ListDashboards(context.Context, *bcmdashboards.ListDashboardsInput, ...func(*bcmdashboards.Options)) (*bcmdashboards.ListDashboardsOutput, error)
	ListScheduledReports(context.Context, *bcmdashboards.ListScheduledReportsInput, ...func(*bcmdashboards.Options)) (*bcmdashboards.ListScheduledReportsOutput, error)
}

func scanBCMDashboards(ctx context.Context, client bcmDashboardsAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanBCMDashboardsDashboards(ctx, client, acct, st, scanID) },
		func() (int, int, error) { return scanBCMDashboardsScheduledReports(ctx, client, acct, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanBCMDashboardsDashboards(ctx context.Context, client bcmDashboardsAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	region := bcmDashboardsRegion
	pager := bcmdashboards.NewListDashboardsPaginator(client, &bcmdashboards.ListDashboardsInput{})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "bcmdashboards:ListDashboards", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("bcmdashboards:ListDashboards: %w", perr)
		}
		for _, d := range out.Dashboards {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			name := sv(d.Name)
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBCMDashboardsDashboard, NativeID: arn,
				Name: &name, Region: regionGlobal, CreatedAt: tp(d.CreatedAt),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, rows, "bcmdashboards dashboards")
}

func scanBCMDashboardsScheduledReports(ctx context.Context, client bcmDashboardsAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	region := bcmDashboardsRegion
	pager := bcmdashboards.NewListScheduledReportsPaginator(client, &bcmdashboards.ListScheduledReportsInput{})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "bcmdashboards:ListScheduledReports", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("bcmdashboards:ListScheduledReports: %w", perr)
		}
		for _, r := range out.ScheduledReports {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			name := sv(r.Name)
			status := string(r.State)
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBCMDashboardsScheduledReport, NativeID: arn,
				Name: &name, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, rows, "bcmdashboards scheduled-reports")
}
