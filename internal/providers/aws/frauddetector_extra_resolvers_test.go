package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveFraudDetectorRuleRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	detARN := fmt.Sprintf("arn:aws:frauddetector:%s:%s:detector/det-1", region, acct.ID)
	detID := upsertTestResource(t, st, "aws", acct.ID, TypeFraudDetectorDetector, detARN, region, "{}")
	ruleARN := fmt.Sprintf("arn:aws:frauddetector:%s:%s:rule/det-1/rule-1/1", region, acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeFraudDetectorRule, ruleARN, region, `{"DetectorId":"det-1"}`)
	if err := resolveFraudDetectorRuleRelationships(acct, st); err != nil {
		t.Fatalf("resolveFraudDetectorRuleRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rels, rID, detID, store.RelAttachedTo)
}

func TestResolveFraudDetectorRuleRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	ruleARN := fmt.Sprintf("arn:aws:frauddetector:%s:%s:rule/det-1/rule-1/1", region, acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeFraudDetectorRule, ruleARN, region, "{}")
	if err := resolveFraudDetectorRuleRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(rID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
