package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveInspectorAssessmentTargetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rgARN := fmt.Sprintf("arn:aws:inspector:%s:%s:resourcegroup/0-rg1", testRegion, acct.ID)
	rgID := upsertTestResource(t, st, "aws", acct.ID, TypeInspectorResourceGroup, rgARN, testRegion, "{}")
	atARN := fmt.Sprintf("arn:aws:inspector:%s:%s:target/0-tgt1", testRegion, acct.ID)
	atID := upsertTestResource(t, st, "aws", acct.ID, TypeInspectorAssessmentTarget, atARN, testRegion,
		fmt.Sprintf(`{"ResourceGroupArn":"%s"}`, rgARN))
	if err := resolveInspectorAssessmentTargetRefs(acct, st); err != nil {
		t.Fatalf("resolveInspectorAssessmentTargetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(atID)
	assertRelationship(t, rels, atID, rgID, store.RelAttachedTo)
}

func TestResolveInspectorAssessmentTemplateRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	atARN := fmt.Sprintf("arn:aws:inspector:%s:%s:target/0-tgt1", testRegion, acct.ID)
	atID := upsertTestResource(t, st, "aws", acct.ID, TypeInspectorAssessmentTarget, atARN, testRegion, "{}")
	tplARN := fmt.Sprintf("arn:aws:inspector:%s:%s:target/0-tgt1/template/0-tpl1", testRegion, acct.ID)
	tplID := upsertTestResource(t, st, "aws", acct.ID, TypeInspectorAssessmentTemplate, tplARN, testRegion,
		fmt.Sprintf(`{"AssessmentTargetArn":"%s"}`, atARN))
	if err := resolveInspectorAssessmentTemplateRefs(acct, st); err != nil {
		t.Fatalf("resolveInspectorAssessmentTemplateRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tplID)
	assertRelationship(t, rels, tplID, atID, store.RelAttachedTo)
}
