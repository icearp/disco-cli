package aws

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2"
	kav2types "github.com/aws/aws-sdk-go-v2/service/kinesisanalyticsv2/types"
	"github.com/icearp/disco-cli/store"
)

// TestScanKinesisAnalyticsV2_OneVersionPerScan: an unchanged application must
// produce one version row however many times it is scanned. The scanner used
// to store the ListApplications summary and then the DescribeApplication
// detail under the same ARN in one scan; with no ON CONFLICT update in the
// store, each differing upsert is a version split, so every scan added two
// rows and reported the application as changed.
func TestScanKinesisAnalyticsV2_OneVersionPerScan(t *testing.T) {
	st := newTestStore(t)
	arn := "arn:aws:kinesisanalytics:us-east-1:123456789012:application/app1"
	name := "app1"
	summary := func() stubCall {
		return stubCall{Output: &kinesisanalyticsv2.ListApplicationsOutput{ApplicationSummaries: []kav2types.ApplicationSummary{{
			ApplicationARN: &arn, ApplicationName: &name, ApplicationStatus: kav2types.ApplicationStatusRunning,
			ApplicationVersionId: sdkaws.Int64(3), RuntimeEnvironment: kav2types.RuntimeEnvironmentFlink118,
		}}}}
	}
	role := "arn:aws:iam::123456789012:role/kda"
	detail := func() stubCall {
		return stubCall{Output: &kinesisanalyticsv2.DescribeApplicationOutput{ApplicationDetail: &kav2types.ApplicationDetail{
			ApplicationARN: &arn, ApplicationName: &name, ApplicationStatus: kav2types.ApplicationStatusRunning,
			ApplicationVersionId: sdkaws.Int64(3), RuntimeEnvironment: kav2types.RuntimeEnvironmentFlink118,
			ServiceExecutionRole: &role,
		}}}
	}
	stub := stubResponses(t, map[string][]stubCall{
		"ListApplications":    {summary(), summary()},
		"DescribeApplication": {detail(), detail()},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

	second, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var newC, changedC atomic.Int64
	for _, scanID := range []string{testScanID, second} {
		if _, _, err := scanKinesisAnalyticsV2(context.Background(), acct, testRegion, st.WithUpsertCounters(&newC, &changedC), scanID); err != nil {
			t.Fatalf("scan %s: %v", scanID, err)
		}
	}

	versions, err := st.GetResourceVersions(store.ResourceID("aws", testAccountID, arn))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Errorf("versions = %d, want 1 (unchanged application scanned twice)", len(versions))
	}
	if newC.Load() != 1 || changedC.Load() != 0 {
		t.Errorf("new=%d changed=%d, want 1/0", newC.Load(), changedC.Load())
	}
	// The stored row is the detail body: resolvers read ServiceExecutionRole.
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.AttributesJSON, `"ServiceExecutionRole"`) {
		t.Errorf("stored attributes lack the detail body: %s", r.AttributesJSON)
	}
}

// TestScanKinesisAnalyticsV2_DeniedDetailKeepsSummary: without
// DescribeApplication the application is still stored, once, from its
// summary — the detail read is an enrichment, not a precondition.
func TestScanKinesisAnalyticsV2_DeniedDetailKeepsSummary(t *testing.T) {
	st := newTestStore(t)
	arn := "arn:aws:kinesisanalytics:us-east-1:123456789012:application/app1"
	name := "app1"
	stub := stubResponses(t, map[string][]stubCall{
		"ListApplications": {{Output: &kinesisanalyticsv2.ListApplicationsOutput{ApplicationSummaries: []kav2types.ApplicationSummary{{
			ApplicationARN: &arn, ApplicationName: &name, ApplicationStatus: kav2types.ApplicationStatusReady,
		}}}}},
		"DescribeApplication": {{Err: apiErr("AccessDeniedException", "not authorized to perform: kinesisanalytics:DescribeApplication")}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

	total, inserted, err := scanKinesisAnalyticsV2(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	versions, err := st.GetResourceVersions(store.ResourceID("aws", testAccountID, arn))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || strings.Contains(versions[0].AttributesJSON, `"ServiceExecutionRole"`) {
		t.Errorf("versions = %+v, want one summary row", versions)
	}
}

// TestScanKinesisAnalyticsV2_FailedDetailStillStoresEveryApplication: a
// detail-read failure on one application must not cost the ones after it
// their rows; each falls back to its summary and the failure is returned.
func TestScanKinesisAnalyticsV2_FailedDetailStillStoresEveryApplication(t *testing.T) {
	st := newTestStore(t)
	arns := []string{
		"arn:aws:kinesisanalytics:us-east-1:123456789012:application/app1",
		"arn:aws:kinesisanalytics:us-east-1:123456789012:application/app2",
	}
	names := []string{"app1", "app2"}
	var sums []kav2types.ApplicationSummary
	for i := range arns {
		sums = append(sums, kav2types.ApplicationSummary{ApplicationARN: &arns[i], ApplicationName: &names[i]})
	}
	stub := stubResponses(t, map[string][]stubCall{
		"ListApplications": {{Output: &kinesisanalyticsv2.ListApplicationsOutput{ApplicationSummaries: sums}}},
		"DescribeApplication": {
			{Err: apiErr("InternalFailure", "boom")},
			{Output: &kinesisanalyticsv2.DescribeApplicationOutput{ApplicationDetail: &kav2types.ApplicationDetail{ApplicationARN: &arns[1], ApplicationName: &names[1]}}},
		},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

	total, _, err := scanKinesisAnalyticsV2(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InternalFailure") {
		t.Errorf("err = %v, want the DescribeApplication InternalFailure", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	for _, arn := range arns {
		if v, verr := st.GetResourceVersions(store.ResourceID("aws", testAccountID, arn)); verr != nil || len(v) != 1 {
			t.Errorf("%s: versions = %d (%v), want 1", arn, len(v), verr)
		}
	}
}
