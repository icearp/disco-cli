package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveForecastPredictorRefs,
		EdgeDecl{TypeForecastPredictor, TypeForecastDatasetGroup, store.RelUses},
	)
	registerResolver(
		resolveForecastForecastRefs,
		EdgeDecl{TypeForecastForecast, TypeForecastDatasetGroup, store.RelUses},
		EdgeDecl{TypeForecastForecast, TypeForecastPredictor, store.RelUses},
	)
	registerResolver(
		resolveForecastMonitorRefs,
		EdgeDecl{TypeForecastMonitor, TypeForecastPredictor, store.RelAttachedTo},
	)
	registerResolver(
		resolveForecastExplainabilityRefs,
		EdgeDecl{TypeForecastExplainability, TypeForecastPredictor, store.RelUses},
		EdgeDecl{TypeForecastExplainability, TypeForecastForecast, store.RelUses},
	)
	registerResolver(
		resolveForecastWhatIfAnalysisRefs,
		EdgeDecl{TypeForecastWhatIfAnalysis, TypeForecastForecast, store.RelUses},
	)
	registerResolver(
		resolveForecastWhatIfForecastRefs,
		EdgeDecl{TypeForecastWhatIfForecast, TypeForecastWhatIfAnalysis, store.RelAttachedTo},
	)
}

// forecastEdge emits one FK-safe edge per srcType row to the target identified
// by the ARN at attrKey, checked against the pre-built target id set.
func forecastEdge(acct *account, st *store.Store, srcType, attrKey, tgtType string, kind string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{srcType}, Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	tgtSet, err := scannedIDSet(acct, st, tgtType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ref, _ := attrs[attrKey].(string)
		if ref == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, ref)
		if tgtSet[tgt] {
			if err := st.UpsertRelationship(r.ID, tgt, kind, "directed", nil); err != nil {
				return fmt.Errorf("upsert %s→%s: %w", srcType, tgtType, err)
			}
		}
	}
	return nil
}

func resolveForecastPredictorRefs(acct *account, st *store.Store) error {
	return forecastEdge(acct, st, TypeForecastPredictor, "DatasetGroupArn", TypeForecastDatasetGroup, store.RelUses)
}

func resolveForecastForecastRefs(acct *account, st *store.Store) error {
	if err := forecastEdge(acct, st, TypeForecastForecast, "DatasetGroupArn", TypeForecastDatasetGroup, store.RelUses); err != nil {
		return err
	}
	return forecastEdge(acct, st, TypeForecastForecast, "PredictorArn", TypeForecastPredictor, store.RelUses)
}

func resolveForecastMonitorRefs(acct *account, st *store.Store) error {
	return forecastEdge(acct, st, TypeForecastMonitor, "ResourceArn", TypeForecastPredictor, store.RelAttachedTo)
}

// resolveForecastExplainabilityRefs — ResourceArn is a predictor or forecast; emits to whichever is scanned.
func resolveForecastExplainabilityRefs(acct *account, st *store.Store) error {
	if err := forecastEdge(acct, st, TypeForecastExplainability, "ResourceArn", TypeForecastPredictor, store.RelUses); err != nil {
		return err
	}
	return forecastEdge(acct, st, TypeForecastExplainability, "ResourceArn", TypeForecastForecast, store.RelUses)
}

func resolveForecastWhatIfAnalysisRefs(acct *account, st *store.Store) error {
	return forecastEdge(acct, st, TypeForecastWhatIfAnalysis, "ForecastArn", TypeForecastForecast, store.RelUses)
}

func resolveForecastWhatIfForecastRefs(acct *account, st *store.Store) error {
	return forecastEdge(acct, st, TypeForecastWhatIfForecast, "WhatIfAnalysisArn", TypeForecastWhatIfAnalysis, store.RelAttachedTo)
}
