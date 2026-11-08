package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
	cetypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCEAnomalyMonitor, Service: "ce"})
	registerType(restype.Descriptor{Type: TypeCEAnomalySubscription, Service: "ce"})
	registerType(restype.Descriptor{Type: TypeCECostCategory, Service: "ce"})
	registerService(serviceEntry{
		name:   "aws:ce",
		global: true,
		fn:     scanCostExplorer,
	})
}

type costExplorerAPI interface {
	GetAnomalyMonitors(context.Context, *costexplorer.GetAnomalyMonitorsInput, ...func(*costexplorer.Options)) (*costexplorer.GetAnomalyMonitorsOutput, error)
	GetAnomalySubscriptions(context.Context, *costexplorer.GetAnomalySubscriptionsInput, ...func(*costexplorer.Options)) (*costexplorer.GetAnomalySubscriptionsOutput, error)
	ListCostCategoryDefinitions(context.Context, *costexplorer.ListCostCategoryDefinitionsInput, ...func(*costexplorer.Options)) (*costexplorer.ListCostCategoryDefinitionsOutput, error)
	ListTagsForResource(context.Context, *costexplorer.ListTagsForResourceInput, ...func(*costexplorer.Options)) (*costexplorer.ListTagsForResourceOutput, error)
}

const (
	ceTagsOp = "ce:ListTagsForResource"
	// ceCostCategoryPageCap is ListCostCategoryDefinitions' MaxResults
	// maximum. The anomaly listings model no MaxResults range and their docs
	// name no cap, so they are sent none.
	ceCostCategoryPageCap = 100
)

// scanCostExplorer discovers anomaly monitors, anomaly subscriptions, and
// cost category definitions. CE is global, called in its home region
// (homeRegion; us-east-1 for aws).
func scanCostExplorer(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total int, err error) {
	region, err := acct.homeRegion("costexplorer")
	if err != nil {
		return 0, err
	}
	client := costexplorer.NewFromConfig(acct.cfg, func(o *costexplorer.Options) { o.Region = region })
	return scanCostExplorerAll(ctx, client, acct, region, st, scanID)
}

// scanCostExplorerAll stores the anomaly monitors, anomaly subscriptions and
// cost categories, each with its tags (ceTags). Every listing runs as its own
// phase, one at a time and in that order: a phase's error does not stop the
// later ones; a failed write and ctx's end do (runHostPhases). The tag
// reads' denials are one warning for the whole scan.
//
// A listing whose first page Cost Explorer refuses as not enabled for the
// account (isCostExplorerNotEnabled) is read as empty without a warning: the
// account cannot fix that with IAM. That covers a linked account refused
// cost categories ("doesn't have access to cost category") while its other
// listings answer: its Cost Explorer is enabled, and it cannot enable cost
// categories itself, so that refusal never reports the account disabled.
// The scan reports the account disabled only when Cost Explorer itself was
// refused as not enabled (isCostExplorerUserNotEnabled) while nothing was
// stored and no phase failed, so the sentinel never discards rows or hides
// an error. The same refusal of a later page is warned about as a denial
// under its listing, with the pages before it kept.
func scanCostExplorerAll(ctx context.Context, client costExplorerAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	// The phases run one at a time, so the gate is never called concurrently.
	var notEnabled error
	gate := func(err error) bool {
		if !isCostExplorerNotEnabled(err) {
			return false
		}
		if isCostExplorerUserNotEnabled(err) {
			notEnabled = err
		}
		return true
	}
	tags := ceTags(client).sharingDenials()
	total, err := runHostPhases(ctx, st, acct, region, "ce", 0, []hostPhase{
		{op: "ce:GetAnomalyMonitors", run: func(p *phaseTotals) bool {
			return scanCEAnomalyMonitors(ctx, client, acct, region, st, scanID, gate, tags, p)
		}},
		{op: "ce:GetAnomalySubscriptions", run: func(p *phaseTotals) bool {
			return scanCEAnomalySubscriptions(ctx, client, acct, region, st, scanID, gate, tags, p)
		}},
		{op: "ce:ListCostCategoryDefinitions", run: func(p *phaseTotals) bool {
			return scanCECostCategories(ctx, client, acct, region, st, scanID, gate, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	if notEnabled != nil && total == 0 && err == nil {
		return 0, markServiceDisabled(notEnabled)
	}
	return total, err
}

// ceTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference lists
// anomalymonitor, anomalysubscription and costcategory under
// ListTagsForResource, and each one's Create op takes ResourceTags. The op
// returns every tag at once. ResourceNotFoundException, its modeled
// not-found, drops a resource deleted after it was listed.
// LimitExceededException, its only other modeled error, is Cost Explorer's
// throttle (ceThrottled).
func ceTags(client costExplorerAPI) rowTagReader {
	return rowTagReader{
		op:       ceTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &costexplorer.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, ceThrottled(err)
			}
			return kvTagsJSON(out.ResourceTags, func(t cetypes.ResourceTag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// ceThrottled is err marked transient (throttledAfterRetriesError) when it is
// LimitExceededException, else err. Every Cost Explorer op the scan calls
// models LimitExceededException as Cost Explorer's throttle ("You made too
// many calls in a short period of time"), so one still returned after the
// SDK's retries makes the scan partial, not failed.
func ceThrottled(err error) error {
	if isAPIErrorCode(err, "LimitExceededException") {
		return &throttledAfterRetriesError{err}
	}
	return err
}

// ceRow is the row of a listed element stored under its ARN and named name;
// an element without an ARN is not stored (arnRow), and what names such
// elements in the listing's warning.
func ceRow(acct *account, scanID, typ, what string, arn, name *string, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: regionGlobal,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanCEAnomalyMonitors stores the anomaly monitors, each page written as
// soon as its rows' tags are read. A first page notEnabled accepts is read
// as an empty listing. It reports whether a write failed, after which the
// scan must stop.
func scanCEAnomalyMonitors(ctx context.Context, client costExplorerAPI, acct *account, region string, st *store.Store, scanID string, notEnabled func(error) bool, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "ce:GetAnomalyMonitors",
		firstPageGapPages(notEnabled, func(ctx context.Context, token *string) ([]cetypes.AnomalyMonitor, *string, error) {
			out, err := client.GetAnomalyMonitors(ctx, &costexplorer.GetAnomalyMonitorsInput{NextPageToken: token})
			if err != nil {
				return nil, nil, ceThrottled(err)
			}
			return out.AnomalyMonitors, out.NextPageToken, nil
		}),
		func(m cetypes.AnomalyMonitor) (*store.Resource, string, string) {
			return ceRow(acct, scanID, TypeCEAnomalyMonitor, "anomaly monitors", m.MonitorArn, m.MonitorName, m)
		},
		tags, p)
}

// scanCEAnomalySubscriptions stores the anomaly subscriptions, each page
// written as soon as its rows' tags are read. A first page notEnabled
// accepts is read as an empty listing. It reports whether a write failed,
// after which the scan must stop.
func scanCEAnomalySubscriptions(ctx context.Context, client costExplorerAPI, acct *account, region string, st *store.Store, scanID string, notEnabled func(error) bool, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "ce:GetAnomalySubscriptions",
		firstPageGapPages(notEnabled, func(ctx context.Context, token *string) ([]cetypes.AnomalySubscription, *string, error) {
			out, err := client.GetAnomalySubscriptions(ctx, &costexplorer.GetAnomalySubscriptionsInput{NextPageToken: token})
			if err != nil {
				return nil, nil, ceThrottled(err)
			}
			return out.AnomalySubscriptions, out.NextPageToken, nil
		}),
		func(s cetypes.AnomalySubscription) (*store.Resource, string, string) {
			return ceRow(acct, scanID, TypeCEAnomalySubscription, "anomaly subscriptions", s.SubscriptionArn, s.SubscriptionName, s)
		},
		tags, p)
}

// scanCECostCategories stores the cost categories, each page written as
// soon as its rows' tags are read. A first page notEnabled accepts is read
// as an empty listing. It reports whether a write failed, after which the
// scan must stop.
func scanCECostCategories(ctx context.Context, client costExplorerAPI, acct *account, region string, st *store.Store, scanID string, notEnabled func(error) bool, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "ce:ListCostCategoryDefinitions",
		firstPageGapPages(notEnabled, func(ctx context.Context, token *string) ([]cetypes.CostCategoryReference, *string, error) {
			out, err := client.ListCostCategoryDefinitions(ctx, &costexplorer.ListCostCategoryDefinitionsInput{MaxResults: sdkaws.Int32(ceCostCategoryPageCap), NextToken: token})
			if err != nil {
				return nil, nil, ceThrottled(err)
			}
			return out.CostCategoryReferences, out.NextToken, nil
		}),
		func(c cetypes.CostCategoryReference) (*store.Resource, string, string) {
			return ceRow(acct, scanID, TypeCECostCategory, "cost categories", c.CostCategoryArn, c.Name, c)
		},
		tags, p)
}
