package aws

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehub"
	resiliencehubtypes "github.com/aws/aws-sdk-go-v2/service/resiliencehub/types"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehubv2"
	"github.com/icearp/disco-cli/store"
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

func TestRHInputSourceKey(t *testing.T) {
	const (
		clusterARN = "arn:aws:eks:us-east-1:123456789012:cluster/c1"
		stateURL   = "s3://bucket/path/terraform.tfstate"
	)
	eks := func(cluster, namespace string) *resiliencehubtypes.EksSourceClusterNamespace {
		return &resiliencehubtypes.EksSourceClusterNamespace{EksClusterArn: sdkaws.String(cluster), Namespace: sdkaws.String(namespace)}
	}
	for _, tc := range []struct {
		name string
		src  resiliencehubtypes.AppInputSource
		want string
	}{
		{
			name: "EKS keys on cluster and namespace, not a shared SourceArn",
			src:  resiliencehubtypes.AppInputSource{ImportType: resiliencehubtypes.ResourceMappingTypeEks, SourceArn: sdkaws.String(clusterARN), EksSourceClusterNamespace: eks(clusterARN, "ns1")},
			want: clusterARN + "/ns1",
		},
		{
			name: "EKS without namespace",
			src:  resiliencehubtypes.AppInputSource{ImportType: resiliencehubtypes.ResourceMappingTypeEks, SourceArn: sdkaws.String(clusterARN), EksSourceClusterNamespace: eks(clusterARN, "")},
		},
		{
			name: "EKS without cluster block",
			src:  resiliencehubtypes.AppInputSource{ImportType: resiliencehubtypes.ResourceMappingTypeEks, SourceArn: sdkaws.String(clusterARN)},
		},
		{
			name: "Terraform keys on the state file URL",
			src:  resiliencehubtypes.AppInputSource{ImportType: resiliencehubtypes.ResourceMappingTypeTerraform, SourceArn: sdkaws.String("arn:x"), TerraformSource: &resiliencehubtypes.TerraformSource{S3StateFileUrl: sdkaws.String(stateURL)}},
			want: stateURL,
		},
		{
			name: "Terraform without state file",
			src:  resiliencehubtypes.AppInputSource{ImportType: resiliencehubtypes.ResourceMappingTypeTerraform, SourceArn: sdkaws.String("arn:x")},
		},
		{
			name: "CfnStack keys on SourceArn",
			src:  resiliencehubtypes.AppInputSource{ImportType: resiliencehubtypes.ResourceMappingTypeCfnStack, SourceArn: sdkaws.String("arn:stack")},
			want: "arn:stack",
		},
		{
			name: "CfnStack without SourceArn",
			src:  resiliencehubtypes.AppInputSource{ImportType: resiliencehubtypes.ResourceMappingTypeCfnStack},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rhInputSourceKey(tc.src); got != tc.want {
				t.Errorf("rhInputSourceKey = %q; want %q", got, tc.want)
			}
		})
	}
}

// App input sources are listed for the app's draft version at the model's
// page cap; service functions and service input sources at theirs.
func TestRHFanOuts_RequestDraftAtPageCap(t *testing.T) {
	appARN, serviceARN := rhARN(testAccountID, "app", "a1"), rhARN(testAccountID, "service", "s1")
	stub := &rhV2Stub{}
	acct := newTestAccount(testAccountID)
	st := newTestStore(t)
	if _, _, err := scanRHAppInputSources(context.Background(), rhV1Stub{rhV2Stub: stub}, acct, testRegion, st, testScanID, []string{appARN}); err != nil {
		t.Fatalf("scanRHAppInputSources: %v", err)
	}
	if _, _, err := scanRHServiceFunctions(context.Background(), stub, acct, testRegion, st, testScanID, []string{serviceARN}); err != nil {
		t.Fatalf("scanRHServiceFunctions: %v", err)
	}
	if _, _, err := scanRHInputSources(context.Background(), stub, acct, testRegion, st, testScanID, []string{serviceARN}); err != nil {
		t.Fatalf("scanRHInputSources: %v", err)
	}
	if len(stub.inputs) != 3 {
		t.Fatalf("calls = %d; want 3", len(stub.inputs))
	}
	in := stub.inputs[0].(*resiliencehub.ListAppInputSourcesInput)
	if got := sdkaws.ToString(in.AppVersion); got != "draft" {
		t.Errorf("ListAppInputSources AppVersion = %q; want draft", got)
	}
	if got := sdkaws.ToInt32(in.MaxResults); got != 100 {
		t.Errorf("ListAppInputSources MaxResults = %d; want 100", got)
	}
	if got := sdkaws.ToInt32(stub.inputs[1].(*resiliencehubv2.ListServiceFunctionsInput).MaxResults); got != 100 {
		t.Errorf("ListServiceFunctions MaxResults = %d; want 100", got)
	}
	if got := sdkaws.ToInt32(stub.inputs[2].(*resiliencehubv2.ListInputSourcesInput).MaxResults); got != 100 {
		t.Errorf("ListInputSources MaxResults = %d; want 100", got)
	}
}

// rhClosingStoreAccount opens a store that ListApps closes before the apps
// upsert, and counts ListAppInputSources calls. Not newTestStore: its cleanup
// queries the DB, which a closed store can't serve.
func rhClosingStoreAccount(t *testing.T) (*store.Store, *account, *atomic.Int32) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "closing.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	insertTestScan(t, st, testScanID, "running")
	var inputSourceCalls atomic.Int32
	h := map[string]func(in any) (any, error){
		"ListApps": func(any) (any, error) {
			if err := st.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
			return &resiliencehub.ListAppsOutput{AppSummaries: []resiliencehubtypes.AppSummary{
				{AppArn: sdkaws.String(rhARN(testAccountID, "app", "a1")), Name: sdkaws.String("a1")},
			}}, nil
		},
		"ListAppInputSources": func(any) (any, error) {
			inputSourceCalls.Add(1)
			return &resiliencehub.ListAppInputSourcesOutput{}, nil
		},
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, apiErr("AccessDeniedException", "not stubbed"))}
	return st, acct, &inputSourceCalls
}

// An app whose write failed is not stored, so scanRHApps hands on no ARN for
// it, and the write failure is returned.
func TestScanRHApps_NoARNsWhenUpsertFails(t *testing.T) {
	st, acct, _ := rhClosingStoreAccount(t)
	client := resiliencehub.NewFromConfig(acct.cfg, func(o *resiliencehub.Options) { o.Region = testRegion })
	arns, _, _, err := scanRHApps(context.Background(), client, acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want store.ErrStoreWrite", err)
	}
	if len(arns) != 0 {
		t.Errorf("app ARNs = %v; want none (the apps were never stored)", arns)
	}
}

// The top-level scan never fans out over unstored apps, and the write
// failure reaches the dispatcher as one.
func TestScanResilienceHub_NoInputSourcesWhenAppUpsertFails(t *testing.T) {
	st, acct, inputSourceCalls := rhClosingStoreAccount(t)
	_, _, err := scanResilienceHub(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want store.ErrStoreWrite", err)
	}
	if n := inputSourceCalls.Load(); n != 0 {
		t.Errorf("ListAppInputSources calls = %d; want 0 (the app was never stored)", n)
	}
}
