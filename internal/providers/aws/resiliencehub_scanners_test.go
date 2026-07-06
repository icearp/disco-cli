package aws

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehub"
	resiliencehubtypes "github.com/aws/aws-sdk-go-v2/service/resiliencehub/types"
)

// stubResilienceHub feeds one assessment and records whether
// ListRecommendationTemplates was called with the (server-required) AssessmentArn.
type stubResilienceHub struct {
	resilienceHubAPI // embed: unused methods panic if called
	assessmentARN    string
	sawAssessmentArn *string
}

func (s *stubResilienceHub) ListAppAssessments(_ context.Context, _ *resiliencehub.ListAppAssessmentsInput, _ ...func(*resiliencehub.Options)) (*resiliencehub.ListAppAssessmentsOutput, error) {
	return &resiliencehub.ListAppAssessmentsOutput{
		AssessmentSummaries: []resiliencehubtypes.AppAssessmentSummary{{
			AssessmentArn: sdkaws.String(s.assessmentARN),
		}},
	}, nil
}

func (s *stubResilienceHub) ListRecommendationTemplates(_ context.Context, in *resiliencehub.ListRecommendationTemplatesInput, _ ...func(*resiliencehub.Options)) (*resiliencehub.ListRecommendationTemplatesOutput, error) {
	s.sawAssessmentArn = in.AssessmentArn
	return &resiliencehub.ListRecommendationTemplatesOutput{
		RecommendationTemplates: []resiliencehubtypes.RecommendationTemplate{{
			RecommendationTemplateArn: sdkaws.String("arn:aws:resiliencehub:us-east-1:123456789012:recommendation-template/rt-1"),
			Name:                      sdkaws.String("rt-1"),
		}},
	}, nil
}

// ListRecommendationTemplates requires assessmentArn server-side (SDK v2
// validator omits it); scanner must fan out per assessment, passing the ARN
// — never call with empty input.
func TestScanRHRecommendationTemplates_FansOutWithAssessmentArn(t *testing.T) {
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "test"}
	assessmentARN := "arn:aws:resiliencehub:us-east-1:123456789012:app-assessment/app-1"
	stub := &stubResilienceHub{assessmentARN: assessmentARN}

	arns, _, _, err := scanRHAppAssessments(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanRHAppAssessments: %v", err)
	}
	if len(arns) != 1 || arns[0] != assessmentARN {
		t.Fatalf("assessment ARNs = %v; want [%s]", arns, assessmentARN)
	}

	total, _, err := scanRHRecommendationTemplates(context.Background(), stub, acct, testRegion, st, testScanID, arns)
	if err != nil {
		t.Fatalf("scanRHRecommendationTemplates: %v", err)
	}
	if stub.sawAssessmentArn == nil || *stub.sawAssessmentArn != assessmentARN {
		t.Fatalf("ListRecommendationTemplates AssessmentArn = %v; want %s (empty-input call regressed)", stub.sawAssessmentArn, assessmentARN)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 template", total)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeResilienceHubRecommendationTemplate}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("rows = %d; want 1 recommendation-template row", len(rows))
	}
}

// No assessments → zero API calls, no error, no rows.
func TestScanRHRecommendationTemplates_NoAssessments(t *testing.T) {
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "test"}
	stub := &stubResilienceHub{}
	total, inserted, err := scanRHRecommendationTemplates(context.Background(), stub, acct, testRegion, st, testScanID, nil)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("got (%d,%d,%v); want (0,0,nil)", total, inserted, err)
	}
	if stub.sawAssessmentArn != nil {
		t.Error("ListRecommendationTemplates should not be called with no assessments")
	}
}
