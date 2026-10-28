package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/forecast"
	fctypes "github.com/aws/aws-sdk-go-v2/service/forecast/types"
	"github.com/icearp/disco-cli/store"
)

func scanForecastPredictors(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, forecastListPredictorsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]fctypes.PredictorSummary, *string, error) {
			out, err := client.ListPredictors(ctx, &forecast.ListPredictorsInput{MaxResults: sdkaws.Int32(forecastPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Predictors, out.NextToken, nil
		}),
		func(s fctypes.PredictorSummary) (*store.Resource, string, string) {
			return forecastRow(acct, region, scanID, TypeForecastPredictor, "predictors", sv(s.PredictorArn), s.PredictorName, s.Status, s)
		}, tags, p)
}

func scanForecastForecasts(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, forecastListForecastsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]fctypes.ForecastSummary, *string, error) {
			out, err := client.ListForecasts(ctx, &forecast.ListForecastsInput{MaxResults: sdkaws.Int32(forecastPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Forecasts, out.NextToken, nil
		}),
		func(s fctypes.ForecastSummary) (*store.Resource, string, string) {
			return forecastRow(acct, region, scanID, TypeForecastForecast, "forecasts", sv(s.ForecastArn), s.ForecastName, s.Status, s)
		}, tags, p)
}

func scanForecastExplainabilities(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, forecastListExplainabilitiesOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]fctypes.ExplainabilitySummary, *string, error) {
			out, err := client.ListExplainabilities(ctx, &forecast.ListExplainabilitiesInput{MaxResults: sdkaws.Int32(forecastPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Explainabilities, out.NextToken, nil
		}),
		func(s fctypes.ExplainabilitySummary) (*store.Resource, string, string) {
			return forecastRow(acct, region, scanID, TypeForecastExplainability, "explainabilities", sv(s.ExplainabilityArn), s.ExplainabilityName, s.Status, s)
		}, tags, p)
}

func scanForecastMonitors(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, forecastListMonitorsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]fctypes.MonitorSummary, *string, error) {
			out, err := client.ListMonitors(ctx, &forecast.ListMonitorsInput{MaxResults: sdkaws.Int32(forecastPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Monitors, out.NextToken, nil
		}),
		func(s fctypes.MonitorSummary) (*store.Resource, string, string) {
			return forecastRow(acct, region, scanID, TypeForecastMonitor, "monitors", sv(s.MonitorArn), s.MonitorName, s.Status, s)
		}, tags, p)
}

func scanForecastWhatIfAnalyses(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, forecastListWhatIfAnalysesOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]fctypes.WhatIfAnalysisSummary, *string, error) {
			out, err := client.ListWhatIfAnalyses(ctx, &forecast.ListWhatIfAnalysesInput{MaxResults: sdkaws.Int32(forecastPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WhatIfAnalyses, out.NextToken, nil
		}),
		func(s fctypes.WhatIfAnalysisSummary) (*store.Resource, string, string) {
			return forecastRow(acct, region, scanID, TypeForecastWhatIfAnalysis, "what-if analyses", sv(s.WhatIfAnalysisArn), s.WhatIfAnalysisName, s.Status, s)
		}, tags, p)
}

func scanForecastWhatIfForecasts(ctx context.Context, client forecastAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, forecastListWhatIfForecastsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]fctypes.WhatIfForecastSummary, *string, error) {
			out, err := client.ListWhatIfForecasts(ctx, &forecast.ListWhatIfForecastsInput{MaxResults: sdkaws.Int32(forecastPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WhatIfForecasts, out.NextToken, nil
		}),
		func(s fctypes.WhatIfForecastSummary) (*store.Resource, string, string) {
			return forecastRow(acct, region, scanID, TypeForecastWhatIfForecast, "what-if forecasts", sv(s.WhatIfForecastArn), s.WhatIfForecastName, s.Status, s)
		}, tags, p)
}
