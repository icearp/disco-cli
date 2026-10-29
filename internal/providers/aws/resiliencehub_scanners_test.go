package aws

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehub"
	rhtypes "github.com/aws/aws-sdk-go-v2/service/resiliencehub/types"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehubv2"
	rhv2types "github.com/aws/aws-sdk-go-v2/service/resiliencehubv2/types"
	"github.com/icearp/disco-cli/store"
)

func TestRHInputSourceKey(t *testing.T) {
	const (
		clusterARN = "arn:aws:eks:us-east-1:123456789012:cluster/c1"
		stateURL   = "s3://bucket/path/terraform.tfstate"
	)
	eks := func(cluster, namespace string) *rhtypes.EksSourceClusterNamespace {
		return &rhtypes.EksSourceClusterNamespace{EksClusterArn: sdkaws.String(cluster), Namespace: sdkaws.String(namespace)}
	}
	for _, tc := range []struct {
		name string
		src  rhtypes.AppInputSource
		want string
	}{
		{
			name: "EKS keys on cluster and namespace, not a shared SourceArn",
			src:  rhtypes.AppInputSource{ImportType: rhtypes.ResourceMappingTypeEks, SourceArn: sdkaws.String(clusterARN), EksSourceClusterNamespace: eks(clusterARN, "ns1")},
			want: clusterARN + "/ns1",
		},
		{
			name: "EKS without namespace",
			src:  rhtypes.AppInputSource{ImportType: rhtypes.ResourceMappingTypeEks, SourceArn: sdkaws.String(clusterARN), EksSourceClusterNamespace: eks(clusterARN, "")},
		},
		{
			name: "EKS without cluster block",
			src:  rhtypes.AppInputSource{ImportType: rhtypes.ResourceMappingTypeEks, SourceArn: sdkaws.String(clusterARN)},
		},
		{
			name: "Terraform keys on the state file URL",
			src:  rhtypes.AppInputSource{ImportType: rhtypes.ResourceMappingTypeTerraform, SourceArn: sdkaws.String("arn:x"), TerraformSource: &rhtypes.TerraformSource{S3StateFileUrl: sdkaws.String(stateURL)}},
			want: stateURL,
		},
		{
			name: "Terraform without state file",
			src:  rhtypes.AppInputSource{ImportType: rhtypes.ResourceMappingTypeTerraform, SourceArn: sdkaws.String("arn:x")},
		},
		{
			name: "CfnStack keys on SourceArn",
			src:  rhtypes.AppInputSource{ImportType: rhtypes.ResourceMappingTypeCfnStack, SourceArn: sdkaws.String("arn:stack")},
			want: "arn:stack",
		},
		{
			name: "CfnStack without SourceArn",
			src:  rhtypes.AppInputSource{ImportType: rhtypes.ResourceMappingTypeCfnStack},
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
			return &resiliencehub.ListAppsOutput{AppSummaries: []rhtypes.AppSummary{
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

// An app whose write failed is not stored, so scanRHApps hands on no parent
// for it, and reports the failed write. Its tag read was denied (the stub
// answers no ListTagsForResource), but no warning claims the lost row.
func TestScanRHApps_NoParentsWhenUpsertFails(t *testing.T) {
	st, acct, _ := rhClosingStoreAccount(t)
	warns := rhWarnRecorder(st)
	client := resiliencehub.NewFromConfig(acct.cfg, func(o *resiliencehub.Options) { o.Region = testRegion })
	var p phaseTotals
	parents, failed := scanRHApps(context.Background(), client, acct, testRegion, st, testScanID, rhTags(client), &p)
	_, _, err := p.result(st, acct, testRegion)
	if !failed || !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("failed = %v, err = %v; want true and store.ErrStoreWrite", failed, err)
	}
	if len(parents) != 0 {
		t.Errorf("parents = %v; want none (the apps were never stored)", parents)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %+v; want none after a failed write", w)
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

// errRHNotQueued answers a listing call the test queued no response for; the
// call also fails the test.
var errRHNotQueued = errors.New("no response queued for this call")

// rhListOps is every listing op a scan calls.
var rhListOps = []string{
	"ListApps", "ListResiliencyPolicies", "ListAppAssessments", "ListRecommendationTemplates", "ListAppInputSources",
	"ListPolicies", "ListServices", "ListSystems", "ListTests", "ListUserJourneys", "ListServiceFunctions", "ListInputSources",
}

// rhTaggedOps is the listing of each type stored with its tags, by type.
var rhTaggedOps = map[string]string{
	TypeResilienceHubApp:                    "ListApps",
	TypeResilienceHubResiliencyPolicy:       "ListResiliencyPolicies",
	TypeResilienceHubAppAssessment:          "ListAppAssessments",
	TypeResilienceHubRecommendationTemplate: "ListRecommendationTemplates",
	TypeResilienceHubPolicy:                 "ListPolicies",
	TypeResilienceHubService:                "ListServices",
	TypeResilienceHubSystem:                 "ListSystems",
}

// rhTestTags is the tags the fixture gives the resource arn names.
func rhTestTags(arn string) map[string]string { return map[string]string{"arn": arn} }

// rhCalls answers every op of a scan through opStubConfig: a listing op
// from its queue, popped in call order (a call with nothing queued fails the
// test), and either client's ListTagsForResource by the ARN it is sent:
// tagErrs[arn] when set, else rhTestTags(arn), or no tags when untagged. It
// records every listing call's input and the ARNs each client's tag op was
// sent.
type rhCalls struct {
	t        *testing.T
	mu       sync.Mutex
	queues   map[string][]stubCall
	tagErrs  map[string]error
	untagged bool
	inputs   map[string][]any
	v1Tags   []string
	v2Tags   []string
}

func newRHCalls(t *testing.T, queues map[string][]stubCall, untagged bool) *rhCalls {
	return &rhCalls{t: t, queues: queues, tagErrs: map[string]error{}, untagged: untagged, inputs: map[string][]any{}}
}

func (c *rhCalls) list(op string, in any) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs[op] = append(c.inputs[op], in)
	q := c.queues[op]
	if len(q) == 0 {
		c.t.Errorf("%s call %d: no response queued", op, len(c.inputs[op]))
		return nil, errRHNotQueued
	}
	c.queues[op] = q[1:]
	return q[0].Output, q[0].Err
}

func (c *rhCalls) listTags(in any) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v1, isV1 := in.(*resiliencehub.ListTagsForResourceInput)
	var arn string
	if isV1 {
		arn = sdkaws.ToString(v1.ResourceArn)
		c.v1Tags = append(c.v1Tags, arn)
	} else {
		arn = sdkaws.ToString(in.(*resiliencehubv2.ListTagsForResourceInput).ResourceArn)
		c.v2Tags = append(c.v2Tags, arn)
	}
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	tags := rhTestTags(arn)
	if c.untagged {
		tags = nil
	}
	if isV1 {
		return &resiliencehub.ListTagsForResourceOutput{Tags: tags}, nil
	}
	return &resiliencehubv2.ListTagsForResourceOutput{Tags: tags}, nil
}

func (c *rhCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inputs[op])
}

func (c *rhCalls) handlers() map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, op := range rhListOps {
		h[op] = func(in any) (any, error) { return c.list(op, in) }
	}
	return h
}

// rhScan runs the scan's real entry point for acct in its first region
// against handlers.
func rhScan(ctx context.Context, st *store.Store, acct *account, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct.cfg = opStubConfig(handlers, errRHNotQueued)
	return scanResilienceHub(ctx, acct, acct.Regions[0], st, testScanID)
}

// rhFixture queues one element of every type, ARNs built by arnOf, and
// returns the queues and each type's expected row. Unless bare, the tag op
// answers rhTestTags of each ARN, the resiliency policy and recommendation
// template carry rhTestTags of theirs, and the app and assessment have a
// Status; bare, there are no tags and neither has a Status.
func rhFixture(arnOf func(kind, id string) string, bare bool) (map[string][]stubCall, map[string]rhWant) {
	appARN, rpARN := arnOf("app", "a1"), arnOf("resiliency-policy", "rp1")
	asARN, rtARN := arnOf("app-assessment", "as1"), arnOf("recommendation-template", "rt1")
	polARN, svcARN, sysARN := arnOf("policy", "pol"), arnOf("service", "svc"), arnOf("system", "sys")
	elementTags := func(arn string) map[string]string {
		if bare {
			return nil
		}
		return rhTestTags(arn)
	}
	appStatus, assessmentStatus := rhtypes.AppStatusTypeActive, rhtypes.AssessmentStatusSuccess
	if bare {
		appStatus, assessmentStatus = "", ""
	}
	queues := map[string][]stubCall{
		"ListApps": {{Output: &resiliencehub.ListAppsOutput{AppSummaries: []rhtypes.AppSummary{
			{AppArn: sdkaws.String(appARN), Name: sdkaws.String("a1"), Status: appStatus, CreationTime: &rhCreated},
		}}}},
		"ListResiliencyPolicies": {{Output: &resiliencehub.ListResiliencyPoliciesOutput{ResiliencyPolicies: []rhtypes.ResiliencyPolicy{
			{PolicyArn: sdkaws.String(rpARN), PolicyName: sdkaws.String("rp1"), Tier: rhtypes.ResiliencyPolicyTierCritical, Tags: elementTags(rpARN)},
		}}}},
		"ListAppAssessments": {{Output: &resiliencehub.ListAppAssessmentsOutput{AssessmentSummaries: []rhtypes.AppAssessmentSummary{
			{AssessmentArn: sdkaws.String(asARN), AssessmentName: sdkaws.String("as1"), AssessmentStatus: assessmentStatus, AppArn: sdkaws.String(appARN)},
		}}}},
		"ListRecommendationTemplates": {{Output: &resiliencehub.ListRecommendationTemplatesOutput{RecommendationTemplates: []rhtypes.RecommendationTemplate{
			{RecommendationTemplateArn: sdkaws.String(rtARN), AssessmentArn: sdkaws.String(asARN), Name: sdkaws.String("rt1"), Status: rhtypes.RecommendationTemplateStatusSuccess, Tags: elementTags(rtARN)},
		}}}},
		"ListAppInputSources": {{Output: &resiliencehub.ListAppInputSourcesOutput{AppInputSources: []rhtypes.AppInputSource{
			{ImportType: rhtypes.ResourceMappingTypeCfnStack, SourceArn: sdkaws.String(rhCfnStackARN)},
		}}}},
		"ListPolicies": {{Output: &resiliencehubv2.ListPoliciesOutput{PolicySummaries: []rhv2types.PolicySummary{
			{PolicyArn: sdkaws.String(polARN), Name: sdkaws.String("pol"), CreatedAt: &rhCreated},
		}}}},
		"ListServices": {{Output: &resiliencehubv2.ListServicesOutput{ServiceSummaries: []rhv2types.ServiceSummary{
			{ServiceArn: sdkaws.String(svcARN), Name: sdkaws.String("svc"), AssessmentStatus: rhv2types.AssessmentStatusSuccess, CreatedAt: &rhCreated},
		}}}},
		"ListSystems": {{Output: &resiliencehubv2.ListSystemsOutput{SystemSummaries: []rhv2types.SystemSummary{
			{SystemArn: sdkaws.String(sysARN), SystemId: sdkaws.String("sys"), Name: sdkaws.String("sys"), CreatedAt: &rhCreated},
		}}}},
		"ListTests": {{Output: &resiliencehubv2.ListTestsOutput{Tests: []rhv2types.TestSummary{
			{TestId: sdkaws.String("t1"), ServiceArn: sdkaws.String(svcARN)},
		}}}},
		"ListUserJourneys": {{Output: &resiliencehubv2.ListUserJourneysOutput{UserJourneySummaries: []rhv2types.UserJourneySummary{
			{UserJourneyId: sdkaws.String("uj1"), Name: sdkaws.String("uj")},
		}}}},
		"ListServiceFunctions": {{Output: &resiliencehubv2.ListServiceFunctionsOutput{ServiceFunctions: []rhv2types.ServiceFunction{
			{ServiceFunctionId: sdkaws.String("sf1"), ServiceArn: sdkaws.String(svcARN), Name: sdkaws.String("sf")},
		}}}},
		"ListInputSources": {{Output: &resiliencehubv2.ListInputSourcesOutput{InputSourceSummaries: []rhv2types.InputSourceSummary{
			{InputSourceId: sdkaws.String("is1"), Type: rhv2types.InputSourceTypeCfnStack, CfnStackArn: sdkaws.String(rhCfnStackARN)},
		}}}},
	}
	want := map[string]rhWant{
		TypeResilienceHubApp:                    {nativeID: appARN, typ: TypeResilienceHubApp, name: "a1", status: string(appStatus), idKey: "AppArn", idVal: appARN, undated: true},
		TypeResilienceHubResiliencyPolicy:       {nativeID: rpARN, typ: TypeResilienceHubResiliencyPolicy, name: "rp1", idKey: "PolicyArn", idVal: rpARN, undated: true},
		TypeResilienceHubAppAssessment:          {nativeID: asARN, typ: TypeResilienceHubAppAssessment, name: "as1", status: string(assessmentStatus), idKey: "AssessmentArn", idVal: asARN, undated: true},
		TypeResilienceHubRecommendationTemplate: {nativeID: rtARN, typ: TypeResilienceHubRecommendationTemplate, name: "rt1", idKey: "RecommendationTemplateArn", idVal: rtARN, undated: true},
		TypeResilienceHubAppInputSource:         {nativeID: appARN + "/input-source/" + rhCfnStackARN, typ: TypeResilienceHubAppInputSource, idKey: "SourceArn", idVal: rhCfnStackARN, undated: true},
		TypeResilienceHubPolicy:                 {nativeID: polARN, typ: TypeResilienceHubPolicy, name: "pol", idKey: "PolicyArn", idVal: polARN},
		TypeResilienceHubService:                {nativeID: svcARN, typ: TypeResilienceHubService, name: "svc", status: "SUCCESS", idKey: "ServiceArn", idVal: svcARN},
		TypeResilienceHubSystem:                 {nativeID: sysARN, typ: TypeResilienceHubSystem, name: "sys", idKey: "SystemArn", idVal: sysARN},
		TypeResilienceHubTest:                   {nativeID: svcARN + "/test/t1", typ: TypeResilienceHubTest, idKey: "TestId", idVal: "t1", undated: true},
		TypeResilienceHubUserJourney:            {nativeID: sysARN + "/user-journey/uj1", typ: TypeResilienceHubUserJourney, name: "uj", idKey: "UserJourneyId", idVal: "uj1", undated: true},
		TypeResilienceHubServiceFunction:        {nativeID: svcARN + "/service-function/sf1", typ: TypeResilienceHubServiceFunction, name: "sf", idKey: "ServiceFunctionId", idVal: "sf1", undated: true},
		TypeResilienceHubInputSource:            {nativeID: svcARN + "/input-source/is1", typ: TypeResilienceHubInputSource, idKey: "InputSourceId", idVal: "is1", undated: true},
	}
	return queues, want
}

// rhCommercial builds the fixture's ARNs in the test account and region.
func rhCommercial(kind, id string) string { return rhARN(testAccountID, kind, id) }

// rhWantID is the NativeID of typ's fixture row.
func rhWantID(t *testing.T, want map[string]rhWant, typ string) string {
	t.Helper()
	w, ok := want[typ]
	if !ok {
		t.Fatalf("no fixture row of %s", typ)
	}
	return w.nativeID
}

// Every phase stores its fixture row with its Name, Status, CreatedAt and
// listed element. The types read with each client's ListTagsForResource are
// stored with the tags it answers, the op sent exactly those rows' ARNs;
// resiliency policies and recommendation templates with their element's
// tags; the child types with none. Bare, no type has tags, and an app or
// assessment listed without a Status has none. Every listing of a tagged type is sent MaxResults 100,
// and the recommendation templates are listed for the stored assessment.
func TestScanResilienceHub_StoresEveryPhaseWithTags(t *testing.T) {
	for _, bare := range []bool{false, true} {
		t.Run(map[bool]string{false: "tagged", true: "bare"}[bare], func(t *testing.T) {
			queues, want := rhFixture(rhCommercial, bare)
			c := newRHCalls(t, queues, bare)
			st := newTestStore(t)
			warns := rhWarnRecorder(st)

			total, inserted, err := rhScan(context.Background(), st, newTestAccount(testAccountID), c.handlers())
			if err != nil {
				t.Fatalf("scanResilienceHub: %v", err)
			}
			if total != len(want) || inserted != len(want) {
				t.Errorf("total, inserted = %d, %d; want %d, %d (one row per type)", total, inserted, len(want), len(want))
			}
			for typ, w := range want {
				rows := listNetExtraRows(t, st, typ)
				if len(rows) != 1 {
					t.Errorf("%s rows = %d; want 1", typ, len(rows))
					continue
				}
				assertRHRow(t, rows, w)
				got := rows[w.nativeID].TagsJSON
				if _, tagged := rhTaggedOps[typ]; tagged && !bare {
					assertTagsMap(t, got, rhTestTags(w.nativeID))
				} else if got != nil {
					t.Errorf("%s: TagsJSON = %q; want nil", typ, *got)
				}
			}
			wantV1 := []string{want[TypeResilienceHubApp].nativeID, want[TypeResilienceHubAppAssessment].nativeID}
			wantV2 := []string{want[TypeResilienceHubPolicy].nativeID, want[TypeResilienceHubService].nativeID, want[TypeResilienceHubSystem].nativeID}
			if got := slices.Sorted(slices.Values(c.v1Tags)); !slices.Equal(got, slices.Sorted(slices.Values(wantV1))) {
				t.Errorf("v1 ListTagsForResource ARNs = %v; want %v", got, wantV1)
			}
			if got := slices.Sorted(slices.Values(c.v2Tags)); !slices.Equal(got, slices.Sorted(slices.Values(wantV2))) {
				t.Errorf("v2 ListTagsForResource ARNs = %v; want %v", got, wantV2)
			}
			for _, op := range rhTaggedOps {
				for _, in := range c.inputs[op] {
					if got := bacField[*int32](in, "MaxResults"); sdkaws.ToInt32(got) != 100 {
						t.Errorf("%s MaxResults = %v; want 100", op, got)
					}
				}
			}
			tin := c.inputs["ListRecommendationTemplates"]
			if len(tin) != 1 || sdkaws.ToString(tin[0].(*resiliencehub.ListRecommendationTemplatesInput).AssessmentArn) != want[TypeResilienceHubAppAssessment].nativeID {
				t.Errorf("ListRecommendationTemplates inputs = %+v; want one for %s", tin, want[TypeResilienceHubAppAssessment].nativeID)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %+v; want none", w)
			}
		})
	}
}

// A rescan stores the same rows, tags included, without inserting any.
func TestScanResilienceHub_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	queues, want := rhFixture(rhCommercial, false)
	if _, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), newRHCalls(t, queues, false).handlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	queues, _ = rhFixture(rhCommercial, false)
	total, inserted, err := rhScan(context.Background(), st, newTestAccount(testAccountID), newRHCalls(t, queues, false).handlers())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != len(want) || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, len(want))
	}
}

// emptyRHQueues queues one empty page for every listing a scan makes when
// nothing exists: the child listings are not called.
func emptyRHQueues() map[string][]stubCall {
	return map[string][]stubCall{
		"ListApps":               {{Output: &resiliencehub.ListAppsOutput{}}},
		"ListResiliencyPolicies": {{Output: &resiliencehub.ListResiliencyPoliciesOutput{}}},
		"ListAppAssessments":     {{Output: &resiliencehub.ListAppAssessmentsOutput{}}},
		"ListPolicies":           {{Output: &resiliencehubv2.ListPoliciesOutput{}}},
		"ListServices":           {{Output: &resiliencehubv2.ListServicesOutput{}}},
		"ListSystems":            {{Output: &resiliencehubv2.ListSystemsOutput{}}},
	}
}

// Empty listings: nothing stored, no child listing or tag read (rhCalls
// fails the test on an unqueued listing), no error, no warning.
func TestScanResilienceHub_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	c := newRHCalls(t, emptyRHQueues(), false)
	total, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), c.handlers())
	if err != nil || total != 0 {
		t.Fatalf("total = %d, err = %v; want 0, nil", total, err)
	}
	if len(c.v1Tags)+len(c.v2Tags) != 0 {
		t.Errorf("tag reads = %v, %v; want none", c.v1Tags, c.v2Tags)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %+v; want none", w)
	}
}

var (
	rhTestGone     = apiErr("ResourceNotFoundException", "gone")
	rhTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::1:user/u is not authorized to perform: resiliencehub:ListTagsForResource")
	rhTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	rhTestBad      = apiErr("ValidationException", "bad")
)

// rhWarnServices is the Service of each warning.
func rhWarnServices(w []store.ScanWarning) []string {
	ops := make([]string, len(w))
	for i, x := range w {
		ops[i] = x.Service
	}
	return ops
}

// A failed tag read. ResourceNotFoundException is modeled by both clients'
// op, so a resource deleted since it was listed is dropped silently, and a
// parent dropped so has no children listed. A denial keeps the row untagged
// and warns once under the tag op with the scan's scope; a throttle keeps it
// and is transient; any other error keeps it and is an error.
func TestScanResilienceHub_TagReadFailures(t *testing.T) {
	for _, c := range []struct {
		name     string
		typ      string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		lost     int // rows not stored besides the failed one: its children
		noCalls  []string
		wantKept bool
		warn     bool
	}{
		{name: "app/not found", typ: TypeResilienceHubApp, err: rhTestGone, outcome: -1, lost: 1, noCalls: []string{"ListAppInputSources"}},
		{name: "assessment/not found", typ: TypeResilienceHubAppAssessment, err: rhTestGone, outcome: -1, lost: 1, noCalls: []string{"ListRecommendationTemplates"}},
		{name: "policy/not found", typ: TypeResilienceHubPolicy, err: rhTestGone, outcome: -1},
		{name: "service/not found", typ: TypeResilienceHubService, err: rhTestGone, outcome: -1, lost: 3, noCalls: []string{"ListTests", "ListServiceFunctions", "ListInputSources"}},
		{name: "system/not found", typ: TypeResilienceHubSystem, err: rhTestGone, outcome: -1, lost: 1, noCalls: []string{"ListUserJourneys"}},
		{name: "app/denied", typ: TypeResilienceHubApp, err: rhTestDenied, outcome: -1, wantKept: true, warn: true},
		{name: "system/denied", typ: TypeResilienceHubSystem, err: rhTestDenied, outcome: -1, wantKept: true, warn: true},
		{name: "assessment/transient", typ: TypeResilienceHubAppAssessment, err: rhTestThrottle, outcome: outcomeTransient, code: "ThrottlingException", wantKept: true},
		{name: "service/transient", typ: TypeResilienceHubService, err: rhTestThrottle, outcome: outcomeTransient, code: "ThrottlingException", wantKept: true},
		{name: "app/other error", typ: TypeResilienceHubApp, err: rhTestBad, outcome: outcomeError, code: "ValidationException", wantKept: true},
		{name: "policy/other error", typ: TypeResilienceHubPolicy, err: rhTestBad, outcome: outcomeError, code: "ValidationException", wantKept: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			queues, want := rhFixture(rhCommercial, false)
			calls := newRHCalls(t, queues, false)
			id := rhWantID(t, want, c.typ)
			calls.tagErrs[id] = c.err
			st := newTestStore(t)
			warns := rhWarnRecorder(st)

			total, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), calls.handlers())
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			wantTotal := len(want) - c.lost
			if !c.wantKept {
				wantTotal--
			}
			if total != wantTotal {
				t.Errorf("total = %d; want %d", total, wantTotal)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[id]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			for _, op := range c.noCalls {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times; want 0 (its parent was not stored)", op, n)
				}
			}
			w := warns()
			var wantOps []string
			if c.warn {
				wantOps = []string{rhTagsOp}
			}
			if got := rhWarnServices(w); !slices.Equal(got, wantOps) {
				t.Fatalf("warnings = %+v; want %v", w, wantOps)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads of both clients send one warning for the whole scan; the
// rows are kept.
func TestScanResilienceHub_TagDenialsWarnOncePerScan(t *testing.T) {
	queues, want := rhFixture(rhCommercial, false)
	calls := newRHCalls(t, queues, false)
	calls.tagErrs[rhWantID(t, want, TypeResilienceHubApp)] = rhTestDenied
	calls.tagErrs[rhWantID(t, want, TypeResilienceHubAppAssessment)] = rhTestDenied
	calls.tagErrs[rhWantID(t, want, TypeResilienceHubService)] = rhTestDenied
	st := newTestStore(t)
	warns := rhWarnRecorder(st)

	total, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), calls.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != len(want) {
		t.Errorf("total = %d; want %d", total, len(want))
	}
	if got := rhWarnServices(warns()); !slices.Equal(got, []string{rhTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, rhTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op, on the other
// client, is demoted to one warning, so the hard error decides the outcome.
func TestScanResilienceHub_TagThrottleDemotedBesideHardError(t *testing.T) {
	queues, want := rhFixture(rhCommercial, false)
	calls := newRHCalls(t, queues, false)
	calls.tagErrs[rhWantID(t, want, TypeResilienceHubApp)] = rhTestThrottle
	calls.tagErrs[rhWantID(t, want, TypeResilienceHubSystem)] = rhTestBad
	st := newTestStore(t)
	warns := countOpWarnings(st, rhTagsOp)

	_, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), calls.handlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", rhTagsOp, *warns)
	}
}

// rhWithToken sets out's NextToken (out is an SDK output pointer).
func rhWithToken(out any, token string) any {
	reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(sdkaws.String(token)))
	return out
}

// A listing of a tagged type whose second page fails, or repeats the first
// page's NextToken, keeps the first page's row and sends the token it got;
// its error does not stop the later phases, whose rows are all stored.
func TestScanResilienceHub_ListingEndKeepsPagesAndLaterPhases(t *testing.T) {
	for typ, op := range rhTaggedOps {
		for _, end := range []struct {
			name     string
			token    string
			second   func(first any) stubCall
			wantRepl bool
		}{
			{name: "later page fails", token: "t2", second: func(any) stubCall { return stubCall{Err: rhTestBad} }},
			{name: "token repeats", token: "t1", second: func(first any) stubCall {
				empty := reflect.New(reflect.TypeOf(first).Elem()).Interface()
				return stubCall{Output: rhWithToken(empty, "t1")}
			}, wantRepl: true},
		} {
			t.Run(op+"/"+end.name, func(t *testing.T) {
				queues, want := rhFixture(rhCommercial, false)
				first := rhWithToken(queues[op][0].Output, end.token)
				queues[op] = []stubCall{{Output: first}, end.second(first)}
				calls := newRHCalls(t, queues, false)
				st := newTestStore(t)

				total, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), calls.handlers())
				if end.wantRepl {
					if !errors.Is(err, errRepeatedNextToken) {
						t.Errorf("err = %v; want errRepeatedNextToken", err)
					}
				} else {
					assertAPICode(t, err, "ValidationException")
				}
				if total != len(want) {
					t.Errorf("total = %d; want %d (every row, the first page's included)", total, len(want))
				}
				for wt, w := range want {
					if _, ok := rowsByNativeID(t, st, wt)[w.nativeID]; !ok {
						t.Errorf("%s row %s not stored", wt, w.nativeID)
					}
				}
				in := calls.inputs[op]
				if len(in) != 2 || sdkaws.ToString(bacField[*string](in[1], "NextToken")) != end.token {
					t.Errorf("%s calls = %d; want 2, the second sent NextToken %q (%s)", op, len(in), end.token, typ)
				}
			})
		}
	}
}

// rhAddARNless appends an element without an ARN to the first page of
// every listing of a tagged type.
func rhAddARNless(t *testing.T, queues map[string][]stubCall) {
	t.Helper()
	name := sdkaws.String("no-arn")
	for _, op := range rhTaggedOps {
		switch out := queues[op][0].Output.(type) {
		case *resiliencehub.ListAppsOutput:
			out.AppSummaries = append(out.AppSummaries, rhtypes.AppSummary{Name: name})
		case *resiliencehub.ListResiliencyPoliciesOutput:
			out.ResiliencyPolicies = append(out.ResiliencyPolicies, rhtypes.ResiliencyPolicy{PolicyName: name})
		case *resiliencehub.ListAppAssessmentsOutput:
			out.AssessmentSummaries = append(out.AssessmentSummaries, rhtypes.AppAssessmentSummary{AssessmentName: name})
		case *resiliencehub.ListRecommendationTemplatesOutput:
			out.RecommendationTemplates = append(out.RecommendationTemplates, rhtypes.RecommendationTemplate{Name: name})
		case *resiliencehubv2.ListPoliciesOutput:
			out.PolicySummaries = append(out.PolicySummaries, rhv2types.PolicySummary{Name: name})
		case *resiliencehubv2.ListServicesOutput:
			out.ServiceSummaries = append(out.ServiceSummaries, rhv2types.ServiceSummary{Name: name})
		case *resiliencehubv2.ListSystemsOutput:
			out.SystemSummaries = append(out.SystemSummaries, rhv2types.SystemSummary{SystemId: name, Name: name})
		default:
			t.Fatalf("%s: unexpected output %T", op, out)
		}
	}
}

// An element listed without its ARN is not stored and not sent to the tag
// op; each listing warns once about its own.
func TestScanResilienceHub_ElementsWithoutARNWarn(t *testing.T) {
	queues, want := rhFixture(rhCommercial, false)
	rhAddARNless(t, queues)
	calls := newRHCalls(t, queues, false)
	st := newTestStore(t)
	warns := rhWarnRecorder(st)

	total, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), calls.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != len(want) {
		t.Errorf("total = %d; want %d (the ARN-less elements not stored)", total, len(want))
	}
	var wantOps []string
	for _, op := range rhTaggedOps {
		wantOps = append(wantOps, "resiliencehub:"+op)
	}
	if got := slices.Sorted(slices.Values(rhWarnServices(warns()))); !slices.Equal(got, slices.Sorted(slices.Values(wantOps))) {
		t.Errorf("warnings = %v; want one under each of %v", got, wantOps)
	}
	if n := len(calls.v1Tags) + len(calls.v2Tags); n != 5 {
		t.Errorf("tag reads = %d (%v, %v); want 5, the stored rows only", n, calls.v1Tags, calls.v2Tags)
	}
}

// A GovCloud or China ARN reaches each client's tag op unchanged, and the v2
// rows homed in that partition's region are stored.
func TestScanResilienceHub_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			arnOf := func(kind, id string) string {
				return partitionARN(pf.partition, "resiliencehub", pf.region, kind+"/"+id)
			}
			queues, want := rhFixture(arnOf, false)
			calls := newRHCalls(t, queues, false)
			st := newTestStore(t)

			total, _, err := rhScan(context.Background(), st, partitionAccount(pf.partition), calls.handlers())
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			wantV1 := []string{arnOf("app", "a1"), arnOf("app-assessment", "as1")}
			wantV2 := []string{arnOf("policy", "pol"), arnOf("service", "svc"), arnOf("system", "sys")}
			if got := slices.Sorted(slices.Values(calls.v1Tags)); !slices.Equal(got, slices.Sorted(slices.Values(wantV1))) {
				t.Errorf("v1 ListTagsForResource ARNs = %v; want %v", got, wantV1)
			}
			if got := slices.Sorted(slices.Values(calls.v2Tags)); !slices.Equal(got, slices.Sorted(slices.Values(wantV2))) {
				t.Errorf("v2 ListTagsForResource ARNs = %v; want %v", got, wantV2)
			}
			for typ := range rhTaggedOps {
				id := rhWantID(t, want, typ)
				r, ok := rowsByNativeID(t, st, typ)[id]
				if !ok {
					t.Errorf("%s row %s not stored", typ, id)
					continue
				}
				assertTagsMap(t, r.TagsJSON, rhTestTags(id))
			}
		})
	}
}

// Recommendation templates are listed per stored assessment: one
// assessment's failure costs the other none of its rows. A denial warns once
// under the op; any other error, a not-found included, is returned.
func TestScanResilienceHub_PerAssessmentTemplateFailures(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		warn bool
		code string
	}{
		{name: "denied", err: rhTestDenied, warn: true},
		{name: "other error", err: rhTestBad, code: "ValidationException"},
		// The op models no not-found, so an assessment deleted since it was
		// listed is an error, not a skip.
		{name: "not found", err: rhTestGone, code: "ResourceNotFoundException"},
	} {
		t.Run(c.name, func(t *testing.T) {
			queues, want := rhFixture(rhCommercial, false)
			as1, as2 := rhWantID(t, want, TypeResilienceHubAppAssessment), rhCommercial("app-assessment", "as2")
			assessments := queues["ListAppAssessments"][0].Output.(*resiliencehub.ListAppAssessmentsOutput)
			assessments.AssessmentSummaries = append(assessments.AssessmentSummaries, rhtypes.AppAssessmentSummary{
				AssessmentArn: sdkaws.String(as2), AssessmentStatus: rhtypes.AssessmentStatusFailed,
			})
			templates := queues["ListRecommendationTemplates"][0].Output
			calls := newRHCalls(t, queues, false)
			handlers := calls.handlers()
			handlers["ListRecommendationTemplates"] = func(in any) (any, error) {
				switch sdkaws.ToString(in.(*resiliencehub.ListRecommendationTemplatesInput).AssessmentArn) {
				case as1:
					return templates, nil
				case as2:
					return nil, c.err
				}
				t.Errorf("ListRecommendationTemplates for an unlisted assessment: %+v", in)
				return nil, errRHNotQueued
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, rhListRecommendationTemplatesOp)

			total, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), handlers)
			if c.warn {
				if err != nil {
					t.Errorf("err = %v; want nil", err)
				}
				if *warns != 1 {
					t.Errorf("%s warnings = %d; want 1", rhListRecommendationTemplatesOp, *warns)
				}
			} else {
				assertAPICode(t, err, c.code)
				if *warns != 0 {
					t.Errorf("%s warnings = %d; want 0", rhListRecommendationTemplatesOp, *warns)
				}
			}
			if total != len(want)+1 {
				t.Errorf("total = %d; want %d (the second assessment included)", total, len(want)+1)
			}
			if _, ok := rowsByNativeID(t, st, TypeResilienceHubRecommendationTemplate)[rhWantID(t, want, TypeResilienceHubRecommendationTemplate)]; !ok {
				t.Error("the readable assessment's template was not stored")
			}
		})
	}
}

// Once ctx ends during a tag-read listing's second page, the stop is one
// member of the error, although the phase in flight saw it in its listing;
// the first page's row was written with its tags before the second page was
// asked for, and no later phase runs.
func TestScanResilienceHub_ContextEndReportedOnce(t *testing.T) {
	for _, tc := range []struct{ typ, op, next string }{
		{typ: TypeResilienceHubApp, op: "ListApps", next: "ListResiliencyPolicies"},
		{typ: TypeResilienceHubAppAssessment, op: "ListAppAssessments", next: "ListRecommendationTemplates"},
		{typ: TypeResilienceHubPolicy, op: "ListPolicies", next: "ListServices"},
		{typ: TypeResilienceHubService, op: "ListServices", next: "ListSystems"},
		{typ: TypeResilienceHubSystem, op: "ListSystems", next: "ListTests"},
	} {
		t.Run(tc.op, func(t *testing.T) {
			queues, want := rhFixture(rhCommercial, false)
			queues[tc.op][0].Output = rhWithToken(queues[tc.op][0].Output, "t2")
			calls := newRHCalls(t, queues, false)
			handlers := calls.handlers()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			list := handlers[tc.op]
			handlers[tc.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					cancel()
					return nil, context.Canceled
				}
				return list(in)
			}
			st := newTestStore(t)

			_, _, err := rhScan(ctx, st, newTestAccount(testAccountID), handlers)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v; want context.Canceled", err)
			}
			stops := 0
			for _, m := range bacTopMembers(err) {
				if errors.Is(m, context.Canceled) {
					stops++
				}
			}
			if stops != 1 {
				t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
			}
			id := rhWantID(t, want, tc.typ)
			r, ok := rowsByNativeID(t, st, tc.typ)[id]
			if !ok {
				t.Fatalf("first page's %s row %s not stored", tc.typ, id)
			}
			assertTagsMap(t, r.TagsJSON, rhTestTags(id))
			if n := calls.count(tc.next); n != 0 {
				t.Errorf("%s called %d times after ctx ended", tc.next, n)
			}
		})
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanResilienceHub_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "resiliencehub.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		queues, want := rhFixture(rhCommercial, false)
		queues["ListSystems"] = []stubCall{{Err: nx}}
		delete(queues, "ListUserJourneys")
		st := newTestStore(t)
		total, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), newRHCalls(t, queues, false).handlers())
		assertGlueOutcome(t, err, outcomeError)
		if wantTotal := len(want) - 2; total != wantTotal {
			t.Errorf("total = %d; want the %d other rows counted", total, wantTotal)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		queues, want := rhFixture(rhCommercial, false)
		calls := newRHCalls(t, queues, false)
		calls.tagErrs[rhWantID(t, want, TypeResilienceHubPolicy)] = nx
		_, _, err := rhScan(context.Background(), newTestStore(t), newTestAccount(testAccountID), calls.handlers())
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newRHCalls(t, map[string][]stubCall{"ListApps": {{Err: nx}}}, false)
		_, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), calls.handlers())
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListResiliencyPolicies"); n != 0 {
			t.Errorf("ListResiliencyPolicies called %d times; want 0 once the region proved unavailable", n)
		}
	})
}

// The child fan-outs run whatever an earlier phase returned, and every
// phase's error reaches the dispatcher under its own op: a transient one
// demoted to one warning instead of buried in another phase's hard error.
func TestScanResilienceHub_PhaseErrors(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	hard := apiErr("ConflictException", "bad")
	app, svc, test := TypeResilienceHubApp, TypeResilienceHubService, TypeResilienceHubTest
	input, fn, svcIn := TypeResilienceHubAppInputSource, TypeResilienceHubServiceFunction, TypeResilienceHubInputSource
	for _, tc := range []struct {
		name           string
		fail           map[string]error
		warnOp         string
		stored, absent []string
	}{
		{
			name: "earlier hard error after tests", fail: map[string]error{"ListUserJourneys": hard},
			stored: []string{app, svc, test, input, fn, svcIn},
		},
		{
			name: "systems hard error does not stop tests", fail: map[string]error{"ListSystems": hard},
			stored: []string{app, svc, test, input, fn, svcIn},
		},
		{
			name: "resiliency policies transient, journeys hard", fail: map[string]error{"ListResiliencyPolicies": throttle, "ListUserJourneys": hard},
			warnOp: rhListResiliencyPoliciesOp, stored: []string{app, svc, test, input, fn, svcIn}, absent: []string{TypeResilienceHubResiliencyPolicy},
		},
		{
			name: "templates transient, journeys hard", fail: map[string]error{"ListRecommendationTemplates": throttle, "ListUserJourneys": hard},
			warnOp: rhListRecommendationTemplatesOp, stored: []string{app, svc, test, input, fn, svcIn}, absent: []string{TypeResilienceHubRecommendationTemplate},
		},
		{
			name: "earlier transient, input sources hard", fail: map[string]error{"ListUserJourneys": throttle, "ListAppInputSources": hard},
			warnOp: rhListUserJourneysOp, stored: []string{app, svc, test, fn, svcIn}, absent: []string{input},
		},
		{
			name: "input sources transient, functions hard", fail: map[string]error{"ListAppInputSources": throttle, "ListServiceFunctions": hard},
			warnOp: rhListAppInputSourcesOp, stored: []string{app, svc, test, svcIn}, absent: []string{input, fn},
		},
		{
			name: "functions transient, input sources hard", fail: map[string]error{"ListServiceFunctions": throttle, "ListAppInputSources": hard},
			warnOp: rhListServiceFunctionsOp, stored: []string{app, svc, test, svcIn}, absent: []string{input, fn},
		},
		{
			name: "service input sources transient, functions hard", fail: map[string]error{"ListInputSources": throttle, "ListServiceFunctions": hard},
			warnOp: rhListInputSourcesOp, stored: []string{app, svc, test, input}, absent: []string{fn, svcIn},
		},
		{
			name: "functions transient, service input sources hard", fail: map[string]error{"ListServiceFunctions": throttle, "ListInputSources": hard},
			warnOp: rhListServiceFunctionsOp, stored: []string{app, svc, test, input}, absent: []string{fn, svcIn},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queues, want := rhFixture(rhCommercial, false)
			for op, err := range tc.fail {
				queues[op] = []stubCall{{Err: err}}
			}
			if _, ok := tc.fail["ListSystems"]; ok {
				delete(queues, "ListUserJourneys")
			}
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.warnOp)

			_, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), newRHCalls(t, queues, false).handlers())
			assertAPICode(t, err, "ConflictException")
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v; want outcomeError", got)
			}
			if tc.warnOp != "" && *warns != 1 {
				t.Errorf("%s warnings = %d; want 1 for the demoted transient", tc.warnOp, *warns)
			}
			for _, typ := range tc.stored {
				if rows := listNetExtraRows(t, st, typ); len(rows) != 1 || rows[want[typ].nativeID].Type != typ {
					t.Errorf("%s rows = %d; want one with NativeID %s", typ, len(rows), want[typ].nativeID)
				}
			}
			for _, typ := range tc.absent {
				if rows := listNetExtraRows(t, st, typ); len(rows) != 0 {
					t.Errorf("%s rows = %d; want none (its phase failed)", typ, len(rows))
				}
			}
		})
	}
}

// rhV1Types are the types the v1 phases store.
var rhV1Types = []string{
	TypeResilienceHubApp, TypeResilienceHubResiliencyPolicy, TypeResilienceHubAppAssessment,
	TypeResilienceHubRecommendationTemplate, TypeResilienceHubAppInputSource,
}

func assertV1RHRows(t *testing.T, st *store.Store, want map[string]rhWant) {
	t.Helper()
	for _, typ := range rhV1Types {
		if rows := listNetExtraRows(t, st, typ); len(rows) != 1 || rows[want[typ].nativeID].Type != typ {
			t.Errorf("%s rows = %d; want one with NativeID %s (every v1 phase must run)", typ, len(rows), want[typ].nativeID)
		}
	}
}

// A hard v2 failure costs the v1 phases none of their rows, and the later v2
// phases still run.
func TestScanResilienceHub_V2ErrorKeepsOtherRows(t *testing.T) {
	queues, want := rhFixture(rhCommercial, false)
	boom := errors.New("boom")
	queues["ListPolicies"] = []stubCall{{Err: boom}}
	st := newTestStore(t)

	_, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), newRHCalls(t, queues, false).handlers())
	if !errors.Is(err, boom) {
		t.Fatalf("want the ListPolicies error to propagate, got %v", err)
	}
	assertV1RHRows(t, st, want)
	if rows := listNetExtraRows(t, st, TypeResilienceHubService); len(rows) != 1 {
		t.Errorf("service rows = %d; want 1 (ListServices still runs)", len(rows))
	}
}

// In a region without the v2 API the scan succeeds with every v1 row and
// makes no v2 call after ListPolicies (rhCalls fails the test on a call with
// nothing queued), whichever not-deployed shape ListPolicies answers.
func TestScanResilienceHub_V2NotDeployedKeepsV1Rows(t *testing.T) {
	for name, gap := range map[string]error{
		"UnknownOperationException": apiErr("UnknownOperationException", ""),
		"NotFoundException":         apiErr("NotFoundException", "Unable to determine service/operation name to be authorized"),
		"HTTP404":                   respErrCode(404),
	} {
		t.Run(name, func(t *testing.T) {
			queues, want := rhFixture(rhCommercial, false)
			queues["ListPolicies"] = []stubCall{{Err: gap}}
			for _, op := range []string{"ListServices", "ListSystems", "ListTests", "ListUserJourneys", "ListServiceFunctions", "ListInputSources"} {
				delete(queues, op)
			}
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRHCalls(t, queues, false)

			if _, _, err := rhScan(context.Background(), st, newTestAccount(testAccountID), calls.handlers()); err != nil {
				t.Fatalf("scanResilienceHub: %v; want nil (a v2 gap is not a failure)", err)
			}
			assertV1RHRows(t, st, want)
			if len(calls.v2Tags) != 0 {
				t.Errorf("v2 tag reads = %v; want none", calls.v2Tags)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %+v; want none", w)
			}
		})
	}
}
