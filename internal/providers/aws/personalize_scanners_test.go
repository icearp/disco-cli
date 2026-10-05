package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/personalize"
	pztypes "github.com/aws/aws-sdk-go-v2/service/personalize/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// stubPzSolutionVersions serves ListSolutionVersions pages in order; every
// other personalizeAPI method panics via the nil embedded interface.
type stubPzSolutionVersions struct {
	personalizeAPI
	pages  []*personalize.ListSolutionVersionsOutput
	err    error
	inputs []*personalize.ListSolutionVersionsInput
}

func (s *stubPzSolutionVersions) ListSolutionVersions(_ context.Context, in *personalize.ListSolutionVersionsInput, _ ...func(*personalize.Options)) (*personalize.ListSolutionVersionsOutput, error) {
	s.inputs = append(s.inputs, in)
	if s.err != nil {
		return nil, s.err
	}
	page := s.pages[len(s.inputs)-1]
	return page, nil
}

func listPzRows(t *testing.T, st *store.Store, rtype string) []store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{rtype}, Limit: util.AllResources, IncludeManaged: true})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	return rows
}

func TestScanPzSolutionVersions_PaginatesAndStores(t *testing.T) {
	const (
		arn1 = "arn:aws:personalize:us-east-1:123456789012:solution/sol-a/v1"
		arn2 = "arn:aws:personalize:us-east-1:123456789012:solution/sol-b/v2"
	)
	stub := &stubPzSolutionVersions{pages: []*personalize.ListSolutionVersionsOutput{
		{
			SolutionVersions: []pztypes.SolutionVersionSummary{
				{SolutionVersionArn: sdkaws.String(arn1), Status: sdkaws.String("ACTIVE"), TrainingMode: pztypes.TrainingModeFull},
				{Status: sdkaws.String("ACTIVE")},
			},
			NextToken: sdkaws.String("page2"),
		},
		{SolutionVersions: []pztypes.SolutionVersionSummary{
			{SolutionVersionArn: sdkaws.String(arn2), TrainingMode: pztypes.TrainingModeUpdate},
		}},
	}}
	st := newTestStore(t)

	total, _, err := scanPzSolutionVersions(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanPzSolutionVersions: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (empty-ARN element skipped)", total)
	}
	if len(stub.inputs) != 2 {
		t.Fatalf("ListSolutionVersions called %d times, want 2", len(stub.inputs))
	}
	if stub.inputs[0].SolutionArn != nil {
		t.Errorf("SolutionArn = %q, want unset (account-wide list)", *stub.inputs[0].SolutionArn)
	}
	if got := sv(stub.inputs[1].NextToken); got != "page2" {
		t.Errorf("second page NextToken = %q, want page2", got)
	}

	byID := map[string]store.Resource{}
	for _, r := range listPzRows(t, st, TypePersonalizeSolutionVersion) {
		byID[r.NativeID] = r
	}
	if len(byID) != 2 {
		t.Fatalf("stored %d rows, want 2: %v", len(byID), byID)
	}
	r1, ok := byID[arn1]
	if !ok {
		t.Fatalf("row %s missing", arn1)
	}
	if r1.Region == nil || *r1.Region != testRegion {
		t.Errorf("Region = %v, want %s", r1.Region, testRegion)
	}
	if r1.Status == nil || *r1.Status != "ACTIVE" {
		t.Errorf("Status = %v, want ACTIVE", r1.Status)
	}
	if !strings.Contains(r1.AttributesJSON, `"TrainingMode":"FULL"`) {
		t.Errorf("attrs missing TrainingMode: %s", r1.AttributesJSON)
	}
	r2, ok := byID[arn2]
	if !ok {
		t.Fatalf("row %s missing (second page)", arn2)
	}
	if r2.Status != nil {
		t.Errorf("Status = %q, want unset when the summary has none", *r2.Status)
	}
}

func TestScanPzSolutionVersions_Empty(t *testing.T) {
	stub := &stubPzSolutionVersions{pages: []*personalize.ListSolutionVersionsOutput{{}}}
	st := newTestStore(t)

	total, inserted, err := scanPzSolutionVersions(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanPzSolutionVersions: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("want (0,0), got (%d,%d)", total, inserted)
	}
	if rows := listPzRows(t, st, TypePersonalizeSolutionVersion); len(rows) != 0 {
		t.Errorf("stored %d rows, want 0", len(rows))
	}
}

func TestScanPzSolutionVersions_AccessDeniedWarns(t *testing.T) {
	stub := &stubPzSolutionVersions{err: apiErr("AccessDeniedException", "not authorized to perform: personalize:ListSolutionVersions")}
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }

	if _, _, err := scanPzSolutionVersions(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("access denied must not error, got %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}
	if warnings[0].Service != "personalize:ListSolutionVersions" {
		t.Errorf("warning Service = %q, want the ListSolutionVersions op", warnings[0].Service)
	}
}

func TestScanPzSolutionVersions_OtherErrorPropagates(t *testing.T) {
	boom := apiErr("InternalServerError", "boom")
	stub := &stubPzSolutionVersions{err: boom}
	st := newTestStore(t)

	_, _, err := scanPzSolutionVersions(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InternalServerError") {
		t.Fatalf("want wrapped InternalServerError, got %v", err)
	}
}

// emptyPzResponses queues one empty page for every List op scanPersonalize
// calls, so a test overrides only the ops it cares about.
func emptyPzResponses() map[string][]stubCall {
	return map[string][]stubCall{
		"ListDatasetGroups":      {{Output: &personalize.ListDatasetGroupsOutput{}}},
		"ListDatasets":           {{Output: &personalize.ListDatasetsOutput{}}},
		"ListSchemas":            {{Output: &personalize.ListSchemasOutput{}}},
		"ListSolutions":          {{Output: &personalize.ListSolutionsOutput{}}},
		"ListCampaigns":          {{Output: &personalize.ListCampaignsOutput{}}},
		"ListEventTrackers":      {{Output: &personalize.ListEventTrackersOutput{}}},
		"ListFilters":            {{Output: &personalize.ListFiltersOutput{}}},
		"ListMetricAttributions": {{Output: &personalize.ListMetricAttributionsOutput{}}},
		"ListRecommenders":       {{Output: &personalize.ListRecommendersOutput{}}},
		"ListRecipes":            {{Output: &personalize.ListRecipesOutput{}}},
		"ListSolutionVersions":   {{Output: &personalize.ListSolutionVersionsOutput{}}},
	}
}

func TestScanPersonalize_StoresSolutionVersions(t *testing.T) {
	const arn = "arn:aws:personalize:us-east-1:123456789012:solution/sol-a/v1"
	responses := emptyPzResponses()
	responses["ListSolutionVersions"] = []stubCall{{Output: &personalize.ListSolutionVersionsOutput{
		SolutionVersions: []pztypes.SolutionVersionSummary{{SolutionVersionArn: sdkaws.String(arn)}},
	}}}
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stubResponses(t, responses), testRegion)}

	if _, _, err := scanPersonalize(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanPersonalize: %v", err)
	}
	rows := listPzRows(t, st, TypePersonalizeSolutionVersion)
	if len(rows) != 1 || rows[0].NativeID != arn {
		t.Fatalf("solution-version rows = %+v, want one with NativeID %s", rows, arn)
	}
}

// A hard ListSolutionVersions failure must not cost the rows of the phases
// that ran before it; this pins the new phase after every pre-existing one.
func TestScanPersonalize_SolutionVersionErrorKeepsEarlierPhases(t *testing.T) {
	const recipeARN = "arn:aws:personalize:::recipe/aws-user-personalization"
	responses := emptyPzResponses()
	responses["ListRecipes"] = []stubCall{{Output: &personalize.ListRecipesOutput{
		Recipes: []pztypes.RecipeSummary{{RecipeArn: sdkaws.String(recipeARN)}},
	}}}
	boom := errors.New("boom")
	responses["ListSolutionVersions"] = []stubCall{{Err: boom}}
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stubResponses(t, responses), testRegion)}

	_, _, err := scanPersonalize(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, boom) {
		t.Fatalf("want the ListSolutionVersions error to propagate, got %v", err)
	}
	if rows := listPzRows(t, st, TypePersonalizeRecipe); len(rows) != 1 {
		t.Errorf("recipe rows = %d, want 1 (recipes phase must run before solution versions)", len(rows))
	}
}
