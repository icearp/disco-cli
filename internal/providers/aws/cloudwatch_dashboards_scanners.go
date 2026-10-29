package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// CloudWatch dashboards are global: PutDashboard's doc says "All dashboards
// in your account are global, not region-specific", and the Service
// Reference's dashboard ARN carries no region. Scanned in every region, each
// region would read and write the same row, and a region whose GetDashboard
// or tag read failed would overwrite the row another region had read in
// full. They are scanned once per account instead, in CloudWatch's
// once-region (onceRegion), as a global service: --regions does not decide
// whether they are scanned, and --skip-globals skips them.
func init() {
	registerType(restype.Descriptor{Type: TypeCloudWatchDashboard, Service: "cloudwatch"})
	registerService(serviceEntry{
		name:   "aws:cloudwatch-dashboards",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
			return scanCloudWatchDashboards(ctx, acct, st, scanID)
		},
	})
}

const (
	cwListDashboardsOp = "cloudwatch:ListDashboards"
	cwGetDashboardOp   = "cloudwatch:GetDashboard"
)

// cwDashboardNotFound is GetDashboard's not-found error, DashboardNotFoundError,
// by both codes it can arrive under: the awsQuery code the service sends in
// its query-compatible error header (ResourceNotFound) and the shape name
// when that header is absent.
var cwDashboardNotFound = []string{"ResourceNotFound", "DashboardNotFoundError"}

// scanCloudWatchDashboards stores the account's dashboards, read in
// CloudWatch's once-region and stored under the global region.
func scanCloudWatchDashboards(ctx context.Context, acct *account, st *store.Store, scanID string) (int, int, error) {
	region, err := acct.onceRegion("cloudwatch")
	if err != nil {
		return 0, 0, err
	}
	client := cloudwatch.NewFromConfig(acct.cfg, func(o *cloudwatch.Options) { o.Region = region })
	return runHostPhases(ctx, st, acct, sv(regionGlobal), "cloudwatch", 0, []hostPhase{
		{op: cwListDashboardsOp, run: func(p *phaseTotals) bool {
			return scanCWDashboards(ctx, client, acct, st, scanID, cwTags(client), p)
		}},
	})
}

// scanCWDashboards stores the dashboards under their ARNs, a page at a time,
// each row's attributes replaced with its GetDashboard body (DashboardBody).
// A dashboard GetDashboard no longer finds was deleted since the listing and
// is dropped; any other failure keeps the DashboardEntry as the row
// (rowDetailReader). Name comes from the DashboardEntry.
func scanCWDashboards(ctx context.Context, client cloudwatchAPI, acct *account, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	steps := tokenPageSteps{details: rowDetailReader{
		op: cwGetDashboardOp, what: "dashboard(s)", notFound: cwDashboardNotFound,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			out, err := client.GetDashboard(ctx, &cloudwatch.GetDashboardInput{DashboardName: r.Name})
			if err != nil {
				return nil, err
			}
			return out, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, sv(regionGlobal), cwListDashboardsOp,
		func(ctx context.Context, token *string) ([]cwtypes.DashboardEntry, *string, error) {
			out, err := client.ListDashboards(ctx, &cloudwatch.ListDashboardsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DashboardEntries, out.NextToken, nil
		},
		func(d cwtypes.DashboardEntry) (*store.Resource, string, string) {
			name := sv(d.DashboardName)
			return arnRow("dashboards", d.DashboardArn, d.DashboardName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudWatchDashboard, NativeID: sv(d.DashboardArn),
				Name: &name, Region: regionGlobal,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}, steps, tags, p)
}
