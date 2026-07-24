package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveRHAppAssessmentToApp(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	appARN := fmt.Sprintf("arn:aws:resiliencehub:%s:%s:app/app-1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubApp, appARN, testRegion, "{}")
	asARN := fmt.Sprintf("arn:aws:resiliencehub:%s:%s:app-assessment/as-1", testRegion, acct.ID)
	asID := upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubAppAssessment, asARN, testRegion,
		fmt.Sprintf(`{"AssessmentArn":"%s","AppArn":"%s"}`, asARN, appARN))
	if err := resolveRHAppAssessmentToApp(acct, st); err != nil {
		t.Fatalf("resolveRHAppAssessmentToApp: %v", err)
	}
	rels, _ := st.RelationshipsFrom(asID)
	assertRelationship(t, rels, asID, appID, store.RelAttachedTo)
}

func TestResolveRHAppAssessmentToApp_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	asARN := fmt.Sprintf("arn:aws:resiliencehub:%s:%s:app-assessment/as-1", testRegion, acct.ID)
	asID := upsertTestResource(t, st, "aws", acct.ID, TypeResilienceHubAppAssessment, asARN, testRegion, "{}")
	if err := resolveRHAppAssessmentToApp(acct, st); err != nil {
		t.Fatalf("resolveRHAppAssessmentToApp (no attrs): %v", err)
	}
	rels, _ := st.RelationshipsFrom(asID)
	if len(rels) != 0 {
		t.Fatalf("expected no relationships, got %d", len(rels))
	}
}
