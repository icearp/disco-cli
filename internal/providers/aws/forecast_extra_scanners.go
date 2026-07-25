package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/forecast"
	"github.com/icearp/disco-cli/store"
)

func scanForecastPredictors(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := forecast.NewListPredictorsPaginator(client, &forecast.ListPredictorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "forecast:ListPredictors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("forecast:ListPredictors: %w", err)
		}
		for _, p := range out.Predictors {
			arn := sv(p.PredictorArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeForecastPredictor, NativeID: arn,
				Name: p.PredictorName, Region: &region, Status: p.Status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "forecast predictors")
}

func scanForecastForecasts(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := forecast.NewListForecastsPaginator(client, &forecast.ListForecastsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "forecast:ListForecasts", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("forecast:ListForecasts: %w", err)
		}
		for _, f := range out.Forecasts {
			arn := sv(f.ForecastArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeForecastForecast, NativeID: arn,
				Name: f.ForecastName, Region: &region, Status: f.Status,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "forecast forecasts")
}

func scanForecastExplainabilities(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := forecast.NewListExplainabilitiesPaginator(client, &forecast.ListExplainabilitiesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "forecast:ListExplainabilities", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("forecast:ListExplainabilities: %w", err)
		}
		for _, e := range out.Explainabilities {
			arn := sv(e.ExplainabilityArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeForecastExplainability, NativeID: arn,
				Name: e.ExplainabilityName, Region: &region, Status: e.Status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "forecast explainabilities")
}

func scanForecastMonitors(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := forecast.NewListMonitorsPaginator(client, &forecast.ListMonitorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "forecast:ListMonitors", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("forecast:ListMonitors: %w", err)
		}
		for _, m := range out.Monitors {
			arn := sv(m.MonitorArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeForecastMonitor, NativeID: arn,
				Name: m.MonitorName, Region: &region, Status: m.Status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "forecast monitors")
}

func scanForecastWhatIfAnalyses(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := forecast.NewListWhatIfAnalysesPaginator(client, &forecast.ListWhatIfAnalysesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "forecast:ListWhatIfAnalyses", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("forecast:ListWhatIfAnalyses: %w", err)
		}
		for _, w := range out.WhatIfAnalyses {
			arn := sv(w.WhatIfAnalysisArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeForecastWhatIfAnalysis, NativeID: arn,
				Name: w.WhatIfAnalysisName, Region: &region, Status: w.Status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "forecast what-if-analyses")
}

func scanForecastWhatIfForecasts(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := forecast.NewListWhatIfForecastsPaginator(client, &forecast.ListWhatIfForecastsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "forecast:ListWhatIfForecasts", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("forecast:ListWhatIfForecasts: %w", err)
		}
		for _, w := range out.WhatIfForecasts {
			arn := sv(w.WhatIfForecastArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeForecastWhatIfForecast, NativeID: arn,
				Name: w.WhatIfForecastName, Region: &region, Status: w.Status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "forecast what-if-forecasts")
}
