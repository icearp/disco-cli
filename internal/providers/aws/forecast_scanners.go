package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/forecast"
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
}

// scanForecast discovers Forecast datasets and dataset groups.
func scanForecast(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := forecast.NewFromConfig(acct.cfg, func(o *forecast.Options) { o.Region = region })

	t, i, ferr := scanForecastDatasets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanForecastDatasetGroups(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanForecastPredictors(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanForecastForecasts(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanForecastExplainabilities(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanForecastMonitors(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanForecastWhatIfAnalyses(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanForecastWhatIfForecasts(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr = phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanForecastDatasets(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListDatasets(ctx, &forecast.ListDatasetsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "forecast:ListDatasets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("forecast:ListDatasets: %w", err)
		}
		for _, d := range out.Datasets {
			arn := sv(d.DatasetArn)
			if arn == "" {
				continue
			}
			// Enrich with DescribeDataset: list-summary lacks EncryptionConfig
			// (KMSKeyArn/RoleArn) and DataSource; fall back to summary on per-row failure.
			attrs := mustJSON(d)
			darn := arn
			dout, derr := client.DescribeDataset(ctx, &forecast.DescribeDatasetInput{DatasetArn: &darn})
			if derr != nil {
				if isAccessDenied(derr) {
					_ = skipIfAccessDenied(st, "forecast:DescribeDataset", acct.ID, region, derr)
				}
			} else if dout != nil {
				attrs = mustJSON(dout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeForecastDataset, NativeID: arn,
				Name: d.DatasetName, Region: &region,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "forecast datasets")
}

func scanForecastDatasetGroups(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListDatasetGroups(ctx, &forecast.ListDatasetGroupsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "forecast:ListDatasetGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("forecast:ListDatasetGroups: %w", err)
		}
		for _, g := range out.DatasetGroups {
			arn := sv(g.DatasetGroupArn)
			if arn == "" {
				continue
			}
			// Enrich with DescribeDatasetGroup: list-summary lacks DatasetArns[] (member dataset ARNs).
			attrs := mustJSON(g)
			garn := arn
			gout, gerr := client.DescribeDatasetGroup(ctx, &forecast.DescribeDatasetGroupInput{DatasetGroupArn: &garn})
			if gerr != nil {
				if isAccessDenied(gerr) {
					_ = skipIfAccessDenied(st, "forecast:DescribeDatasetGroup", acct.ID, region, gerr)
				}
			} else if gout != nil {
				attrs = mustJSON(gout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeForecastDatasetGroup, NativeID: arn,
				Name: g.DatasetGroupName, Region: &region,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "forecast dataset-groups")
}
