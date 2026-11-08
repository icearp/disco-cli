package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bcmdashboards"
	bcmdtypes "github.com/aws/aws-sdk-go-v2/service/bcmdashboards/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// AWS BCM Dashboards is account-global — for aws accounts endpoints resolve
// only via us-east-1.
const bcmDashboardsRegion = "us-east-1"

const (
	bcmdListDashboardsOp       = "bcmdashboards:ListDashboards"
	bcmdListScheduledReportsOp = "bcmdashboards:ListScheduledReports"
	bcmdTagsOp                 = "bcmdashboards:ListTagsForResource"
	// bcmdPageCap is the maxResults range maximum (1-100) the model gives
	// ListDashboards and ListScheduledReports.
	bcmdPageCap int32 = 100
)

func init() {
	registerType(restype.Descriptor{Type: TypeBCMDashboardsDashboard, Service: "bcmdashboards"})
	registerType(restype.Descriptor{Type: TypeBCMDashboardsScheduledReport, Service: "bcmdashboards"})
	registerService(serviceEntry{
		name:   "aws:bcmdashboards",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, error) {
			region, err := acct.homeRegion("bcmdashboards")
			if err != nil {
				return 0, err
			}
			client := bcmdashboards.NewFromConfig(acct.cfg, func(o *bcmdashboards.Options) { o.Region = region })
			return scanBCMDashboards(ctx, client, acct, st, scanID)
		},
	})
}

// bcmDashboardsAPI is the narrow surface scanBCMDashboards uses.
type bcmDashboardsAPI interface {
	ListDashboards(context.Context, *bcmdashboards.ListDashboardsInput, ...func(*bcmdashboards.Options)) (*bcmdashboards.ListDashboardsOutput, error)
	ListScheduledReports(context.Context, *bcmdashboards.ListScheduledReportsInput, ...func(*bcmdashboards.Options)) (*bcmdashboards.ListScheduledReportsOutput, error)
	ListTagsForResource(context.Context, *bcmdashboards.ListTagsForResourceInput, ...func(*bcmdashboards.Options)) (*bcmdashboards.ListTagsForResourceOutput, error)
}

// scanBCMDashboards discovers the account's dashboards and scheduled
// reports, each with its tags (bcmDashboardsTags). Both listings run as
// phases, one at a time and in that order: a phase's error does not stop
// the later one; a failed write and ctx's end do, and so does a first phase
// that stores nothing and fails only with NXDOMAIN (runHostPhases). Each
// listing is written a page at a time, and the tag reads' denials are one
// warning for the whole scan. The tag reads go through the same client, so
// they reach the listings' endpoint.
func scanBCMDashboards(ctx context.Context, client bcmDashboardsAPI, acct *account, st *store.Store, scanID string) (int, error) {
	region := bcmDashboardsRegion
	tags := bcmDashboardsTags(client).sharingDenials()
	total, err := runHostPhases(ctx, st, acct, region, "bcmdashboards", 0, []hostPhase{
		{op: bcmdListDashboardsOp, run: func(p *phaseTotals) bool {
			return scanBCMDashboardsDashboards(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: bcmdListScheduledReportsOp, run: func(p *phaseTotals) bool {
			return scanBCMDashboardsScheduledReports(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// bcmDashboardsTags reads a dashboard's or scheduled report's tags with
// ListTagsForResource, sent the row's NativeID: the ARN its listed element
// carries. The model's ResourceArn pattern for the op accepts dashboard and
// scheduled-report ARNs, the Service Reference gives both resources
// aws:ResourceTag as a condition key, and CreateDashboard and
// CreateScheduledReport take ResourceTags; no listed element carries them.
// The op returns every tag at once. ResourceNotFoundException, its modeled
// not-found, drops a resource deleted after it was listed.
func bcmDashboardsTags(client bcmDashboardsAPI) rowTagReader {
	return rowTagReader{
		op:       bcmdTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &bcmdashboards.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.ResourceTags, func(t bcmdtypes.ResourceTag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanBCMDashboardsDashboards stores the dashboards, each page written as
// soon as its rows' tags are read. A dashboard can be shared with this
// account from another one (it takes a resource-based policy), and whether
// ListDashboards returns such dashboards is unverified; a dashboard whose ARN
// names another account and whose tag read is denied or not found is kept
// untagged without a warning (untaggedIfOtherAccount). It reports whether a
// write failed, after which the scan must stop.
func scanBCMDashboardsDashboards(ctx context.Context, client bcmDashboardsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, bcmdListDashboardsOp,
		func(ctx context.Context, token *string) ([]bcmdtypes.DashboardReference, *string, error) {
			out, err := client.ListDashboards(ctx, &bcmdashboards.ListDashboardsInput{MaxResults: sdkaws.Int32(bcmdPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Dashboards, out.NextToken, nil
		},
		func(d bcmdtypes.DashboardReference) (*store.Resource, string, string) {
			name := sv(d.Name)
			return arnRow("dashboards", d.Arn, d.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBCMDashboardsDashboard, NativeID: sv(d.Arn),
				Name: &name, Region: regionGlobal, CreatedAt: tp(d.CreatedAt),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}, tags.untaggedIfOtherAccount(acct.ID), p)
}

// scanBCMDashboardsScheduledReports stores the scheduled reports, each page
// written as soon as its rows' tags are read. It reports whether a write
// failed, after which the scan must stop.
func scanBCMDashboardsScheduledReports(ctx context.Context, client bcmDashboardsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, bcmdListScheduledReportsOp,
		func(ctx context.Context, token *string) ([]bcmdtypes.ScheduledReportSummary, *string, error) {
			out, err := client.ListScheduledReports(ctx, &bcmdashboards.ListScheduledReportsInput{MaxResults: sdkaws.Int32(bcmdPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ScheduledReports, out.NextToken, nil
		},
		func(r bcmdtypes.ScheduledReportSummary) (*store.Resource, string, string) {
			name := sv(r.Name)
			status := string(r.State)
			return arnRow("scheduled reports", r.Arn, r.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBCMDashboardsScheduledReport, NativeID: sv(r.Arn),
				Name: &name, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}, tags, p)
}
