package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveForecastForecastRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	dgARN := fmt.Sprintf("arn:aws:forecast:%s:%s:dataset-group/dg-1", region, acct.ID)
	dgID := upsertTestResource(t, st, "aws", acct.ID, TypeForecastDatasetGroup, dgARN, region, "{}")
	prARN := fmt.Sprintf("arn:aws:forecast:%s:%s:predictor/pr-1", region, acct.ID)
	prID := upsertTestResource(t, st, "aws", acct.ID, TypeForecastPredictor, prARN, region, "{}")
	fcARN := fmt.Sprintf("arn:aws:forecast:%s:%s:forecast/fc-1", region, acct.ID)
	attrs := fmt.Sprintf(`{"DatasetGroupArn":"%s","PredictorArn":"%s"}`, dgARN, prARN)
	fcID := upsertTestResource(t, st, "aws", acct.ID, TypeForecastForecast, fcARN, region, attrs)
	if err := resolveForecastForecastRefs(acct, st); err != nil {
		t.Fatalf("resolveForecastForecastRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fcID)
	assertRelationship(t, rels, fcID, dgID, store.RelUses)
	assertRelationship(t, rels, fcID, prID, store.RelUses)
}

func TestResolveForecastForecastRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	fcARN := fmt.Sprintf("arn:aws:forecast:%s:%s:forecast/fc-1", region, acct.ID)
	fcID := upsertTestResource(t, st, "aws", acct.ID, TypeForecastForecast, fcARN, region, "{}")
	if err := resolveForecastForecastRefs(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(fcID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveForecastWhatIfForecastRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	waARN := fmt.Sprintf("arn:aws:forecast:%s:%s:what-if-analysis/wa-1", region, acct.ID)
	waID := upsertTestResource(t, st, "aws", acct.ID, TypeForecastWhatIfAnalysis, waARN, region, "{}")
	wfARN := fmt.Sprintf("arn:aws:forecast:%s:%s:what-if-forecast/wf-1", region, acct.ID)
	wfID := upsertTestResource(t, st, "aws", acct.ID, TypeForecastWhatIfForecast, wfARN, region, fmt.Sprintf(`{"WhatIfAnalysisArn":"%s"}`, waARN))
	if err := resolveForecastWhatIfForecastRefs(acct, st); err != nil {
		t.Fatalf("resolveForecastWhatIfForecastRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(wfID)
	assertRelationship(t, rels, wfID, waID, store.RelAttachedTo)
}
