package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/forecast"
	fctypes "github.com/aws/aws-sdk-go-v2/service/forecast/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeForecastDataset, Service: "forecast"})
	registerType(restype.Descriptor{Type: TypeForecastDatasetGroup, Service: "forecast"})
	registerType(restype.Descriptor{Type: TypeForecastPredictor, Service: "forecast"})
	registerType(restype.Descriptor{Type: TypeForecastForecast, Service: "forecast"})
	registerType(restype.Descriptor{Type: TypeForecastExplainability, Service: "forecast"})
	registerType(restype.Descriptor{Type: TypeForecastMonitor, Service: "forecast"})
	registerType(restype.Descriptor{Type: TypeForecastWhatIfAnalysis, Service: "forecast"})
	registerType(restype.Descriptor{Type: TypeForecastWhatIfForecast, Service: "forecast"})
	registerService(serviceEntry{
		name: "aws:forecast",
		fn:   scanForecast,
	})
}

type forecastAPI interface {
	ListDatasets(context.Context, *forecast.ListDatasetsInput, ...func(*forecast.Options)) (*forecast.ListDatasetsOutput, error)
	ListDatasetGroups(context.Context, *forecast.ListDatasetGroupsInput, ...func(*forecast.Options)) (*forecast.ListDatasetGroupsOutput, error)
	DescribeDataset(context.Context, *forecast.DescribeDatasetInput, ...func(*forecast.Options)) (*forecast.DescribeDatasetOutput, error)
	DescribeDatasetGroup(context.Context, *forecast.DescribeDatasetGroupInput, ...func(*forecast.Options)) (*forecast.DescribeDatasetGroupOutput, error)
	ListPredictors(context.Context, *forecast.ListPredictorsInput, ...func(*forecast.Options)) (*forecast.ListPredictorsOutput, error)
	ListForecasts(context.Context, *forecast.ListForecastsInput, ...func(*forecast.Options)) (*forecast.ListForecastsOutput, error)
	ListExplainabilities(context.Context, *forecast.ListExplainabilitiesInput, ...func(*forecast.Options)) (*forecast.ListExplainabilitiesOutput, error)
	ListMonitors(context.Context, *forecast.ListMonitorsInput, ...func(*forecast.Options)) (*forecast.ListMonitorsOutput, error)
	ListWhatIfAnalyses(context.Context, *forecast.ListWhatIfAnalysesInput, ...func(*forecast.Options)) (*forecast.ListWhatIfAnalysesOutput, error)
	ListWhatIfForecasts(context.Context, *forecast.ListWhatIfForecastsInput, ...func(*forecast.Options)) (*forecast.ListWhatIfForecastsOutput, error)
	ListTagsForResource(context.Context, *forecast.ListTagsForResourceInput, ...func(*forecast.Options)) (*forecast.ListTagsForResourceOutput, error)
}

const (
	forecastListDatasetsOp         = "forecast:ListDatasets"
	forecastListDatasetGroupsOp    = "forecast:ListDatasetGroups"
	forecastListPredictorsOp       = "forecast:ListPredictors"
	forecastListForecastsOp        = "forecast:ListForecasts"
	forecastListExplainabilitiesOp = "forecast:ListExplainabilities"
	forecastListMonitorsOp         = "forecast:ListMonitors"
	forecastListWhatIfAnalysesOp   = "forecast:ListWhatIfAnalyses"
	forecastListWhatIfForecastsOp  = "forecast:ListWhatIfForecasts"
	forecastTagsOp                 = "forecast:ListTagsForResource"
	// forecastPageLimit is the MaxResults range maximum the model gives
	// every list op here.
	forecastPageLimit = 100
)

// forecastNotFound is the not-found code ListTagsForResource,
// DescribeDataset and DescribeDatasetGroup model, for a resource deleted
// after it was listed.
var forecastNotFound = []string{"ResourceNotFoundException"}

func scanForecast(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := forecast.NewFromConfig(acct.cfg, func(o *forecast.Options) { o.Region = region })
	return scanForecastAll(ctx, client, acct, region, st, scanID)
}

// scanForecastAll runs every listing as its own phase, one at a time
// (runHostPhases): a phase's error does not stop the later ones; a failed
// write and ctx's end do. Each listing is written a page at a time with its
// tags (forecastTags), and every tag denial of the scan is one warning.
//
// Amazon Forecast is closed to new customers. A listing whose first page is
// refused as closed (isClosedToNewCustomers: an AccessDeniedException with an
// empty message) is read as empty, silently (firstPageGapPages), since no IAM
// change lifts it. That shape of the closed answer is assumed from the
// iotfleetwise precedent and is unverified for Forecast; if the real answer
// carries a message, each listing warns as a denial.
func scanForecastAll(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := forecastTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "forecast", 0, []hostPhase{
		{op: forecastListDatasetsOp, run: func(p *phaseTotals) bool {
			return scanForecastDatasets(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: forecastListDatasetGroupsOp, run: func(p *phaseTotals) bool {
			return scanForecastDatasetGroups(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: forecastListPredictorsOp, run: func(p *phaseTotals) bool {
			return scanForecastPredictors(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: forecastListForecastsOp, run: func(p *phaseTotals) bool {
			return scanForecastForecasts(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: forecastListExplainabilitiesOp, run: func(p *phaseTotals) bool {
			return scanForecastExplainabilities(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: forecastListMonitorsOp, run: func(p *phaseTotals) bool {
			return scanForecastMonitors(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: forecastListWhatIfAnalysesOp, run: func(p *phaseTotals) bool {
			return scanForecastWhatIfAnalyses(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: forecastListWhatIfForecastsOp, run: func(p *phaseTotals) bool {
			return scanForecastWhatIfForecasts(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// forecastTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the ARN its listing returned. The op does not page. The
// Service Reference lists every type read here as one ListTagsForResource
// takes, and each type's Create op takes Tags.
func forecastTags(client forecastAPI) rowTagReader {
	return rowTagReader{
		op:       forecastTagsOp,
		notFound: forecastNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &forecast.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t fctypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// forecastRow is the row of a listed element of typ keyed by its ARN. An
// element without an ARN is not stored; what names the elements in the
// listing's one warning.
func forecastRow(acct *account, region, scanID, typ, what, arn string, name, status *string, item any) (*store.Resource, string, string) {
	if arn == "" {
		return nil, what + " have no ARN", cmp.Or(sv(name), "(an element with no name)")
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: name, Region: &region, Status: status,
		AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// forecastDetails replaces each listed row with its Describe body, which
// carries what the summary lacks (a dataset's EncryptionConfig, a group's
// DatasetArns). A resource the Describe no longer finds is dropped; any
// other failure keeps the summary as the row (rowDetailReader).
func forecastDetails(op, what string, describe func(ctx context.Context, arn string) (any, error)) tokenPageSteps {
	return tokenPageSteps{details: rowDetailReader{
		op: op, what: what, notFound: forecastNotFound,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			return describe(ctx, r.NativeID)
		},
	}}
}

func scanForecastDatasets(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	steps := forecastDetails("forecast:DescribeDataset", "dataset(s)", func(ctx context.Context, arn string) (any, error) {
		out, err := client.DescribeDataset(ctx, &forecast.DescribeDatasetInput{DatasetArn: &arn})
		if err != nil {
			return nil, err
		}
		return out, nil
	})
	return storeTokenPagesTagsWith(ctx, st, acct, region, forecastListDatasetsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]fctypes.DatasetSummary, *string, error) {
			out, err := client.ListDatasets(ctx, &forecast.ListDatasetsInput{MaxResults: sdkaws.Int32(forecastPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Datasets, out.NextToken, nil
		}),
		func(d fctypes.DatasetSummary) (*store.Resource, string, string) {
			return forecastRow(acct, region, scanID, TypeForecastDataset, "datasets", sv(d.DatasetArn), d.DatasetName, nil, d)
		}, steps, tags, p)
}

func scanForecastDatasetGroups(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	steps := forecastDetails("forecast:DescribeDatasetGroup", "dataset group(s)", func(ctx context.Context, arn string) (any, error) {
		out, err := client.DescribeDatasetGroup(ctx, &forecast.DescribeDatasetGroupInput{DatasetGroupArn: &arn})
		if err != nil {
			return nil, err
		}
		return out, nil
	})
	return storeTokenPagesTagsWith(ctx, st, acct, region, forecastListDatasetGroupsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]fctypes.DatasetGroupSummary, *string, error) {
			out, err := client.ListDatasetGroups(ctx, &forecast.ListDatasetGroupsInput{MaxResults: sdkaws.Int32(forecastPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DatasetGroups, out.NextToken, nil
		}),
		func(g fctypes.DatasetGroupSummary) (*store.Resource, string, string) {
			return forecastRow(acct, region, scanID, TypeForecastDatasetGroup, "dataset groups", sv(g.DatasetGroupArn), g.DatasetGroupName, nil, g)
		}, steps, tags, p)
}
