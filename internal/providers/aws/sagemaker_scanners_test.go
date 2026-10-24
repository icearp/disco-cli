package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/store"
)

// smTags answers ListTags from tags by ResourceArn (no tags for an ARN it
// does not hold), or fails with errs[arn]. It records every ARN asked and
// rejects a call without MaxResults 50. Reads run concurrently.
type smTags struct {
	tags map[string][]smtypes.Tag
	errs map[string]error

	slots chan struct{}

	mu   sync.Mutex
	arns []string
}

func (s *smTags) tagReadSlots() chan struct{} { return s.slots }

func (s *smTags) ListTags(_ context.Context, in *sagemaker.ListTagsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListTagsOutput, error) {
	if got := sdkaws.ToInt32(in.MaxResults); got != 50 {
		return nil, fmt.Errorf("stub ListTags MaxResults = %d; want 50", got)
	}
	arn := sdkaws.ToString(in.ResourceArn)
	s.mu.Lock()
	s.arns = append(s.arns, arn)
	s.mu.Unlock()
	if err := s.errs[arn]; err != nil {
		return nil, err
	}
	return &sagemaker.ListTagsOutput{Tags: s.tags[arn]}, nil
}

// asked returns the ARNs ListTags was asked for, sorted.
func (s *smTags) asked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(slices.Values(s.arns))
}

// smTagMode is what a family scan's ListTags stub answers.
type smTagMode int

const (
	// smTagged gives each resource the one tag {"owner": its ARN}, so a row
	// stored with another row's tags fails too.
	smTagged smTagMode = iota
	// smUntagged gives every resource no tags.
	smUntagged
	// smFirstTagReadFails is smTagged except that the first ARN's read fails
	// with a hard error: the family must still run every later phase.
	smFirstTagReadFails
)

var smTagModes = []smTagMode{smTagged, smUntagged, smFirstTagReadFails}

func (m smTagMode) String() string {
	return [...]string{"tagged", "untagged", "first tag read fails"}[m]
}

const smTagReadFailCode = "ValidationException"

func newSMTags(mode smTagMode, arns ...string) *smTags {
	s := &smTags{tags: map[string][]smtypes.Tag{}, slots: make(chan struct{}, fanoutMed)}
	if mode == smUntagged {
		return s
	}
	for _, arn := range arns {
		s.tags[arn] = []smtypes.Tag{{Key: sdkaws.String("owner"), Value: sdkaws.String(arn)}}
	}
	if mode == smFirstTagReadFails {
		s.errs = map[string]error{arns[0]: apiErr(smTagReadFailCode, "bad")}
	}
	return s
}

// checkSMScanErr checks a family scan's error under mode: the failed tag read
// is returned, and nothing else fails.
func checkSMScanErr(t *testing.T, err error, mode smTagMode) {
	t.Helper()
	if mode == smFirstTagReadFails {
		if err == nil || !everyMember(err, func(m error) bool { return isAPIErrorCode(m, smTagReadFailCode) }) {
			t.Fatalf("scan: %v; want only the failed tag read", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
}

// assertSMTags checks a family scan's tag reads: ListTags was asked exactly
// once for each of arns and for nothing else, and each row stored under arns
// carries the tags mode gave it.
func assertSMTags(t *testing.T, st *store.Store, tags *smTags, mode smTagMode, arns ...string) {
	t.Helper()
	if got, want := tags.asked(), slices.Sorted(slices.Values(arns)); !slices.Equal(got, want) {
		t.Errorf("ListTags ARNs = %v; want %v", got, want)
	}
	for i, arn := range arns {
		r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
		if err != nil {
			t.Fatalf("%s not stored: %v", arn, err)
		}
		if mode == smUntagged || (mode == smFirstTagReadFails && i == 0) {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %s; want nil", arn, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"owner": arn})
	}
}

func smTestARN(kind, name string) string {
	return fmt.Sprintf("arn:aws:sagemaker:%s:%s:%s/%s", testRegion, testAccountID, kind, name)
}

// smTagRows builds rows of the given ARNs for sagemakerStoreTagged.
func smTagRows(arns ...string) []*store.Resource {
	rows := make([]*store.Resource, len(arns))
	for i, arn := range arns {
		rows[i] = &store.Resource{
			Provider: "aws", AccountID: testAccountID, Type: TypeSageMakerModel, NativeID: arn,
			Region: sdkaws.String(testRegion), AttributesJSON: mustJSON(smtypes.ModelSummary{ModelArn: sdkaws.String(arn)}), DiscoveredBy: testScanID,
		}
	}
	return rows
}

// smTagPages serves ListTags in two pages for one ARN, the second asked by the
// first page's token.
type smTagPages struct{ tokens []string }

func (s *smTagPages) tagReadSlots() chan struct{} { return make(chan struct{}, 1) }

func (s *smTagPages) ListTags(_ context.Context, in *sagemaker.ListTagsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListTagsOutput, error) {
	s.tokens = append(s.tokens, sdkaws.ToString(in.NextToken))
	if in.NextToken == nil {
		return &sagemaker.ListTagsOutput{Tags: []smtypes.Tag{{Key: sdkaws.String("a"), Value: sdkaws.String("1")}}, NextToken: sdkaws.String("p2")}, nil
	}
	return &sagemaker.ListTagsOutput{Tags: []smtypes.Tag{{Key: sdkaws.String("b"), Value: sdkaws.String("2")}}}, nil
}

func TestSageMakerStoreTagged_ReadsEveryPage(t *testing.T) {
	st := newTestStore(t)
	arn := smTestARN("model", "m")
	tags := &smTagPages{}
	if _, _, err := sagemakerStoreTagged(context.Background(), tags, newTestAccount(testAccountID), testRegion, st, "sagemaker:ListModels")(smTagRows(arn), nil, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	if want := []string{"", "p2"}; !slices.Equal(tags.tokens, want) {
		t.Errorf("NextTokens sent = %q; want %q", tags.tokens, want)
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeSageMakerModel)[arn].TagsJSON, map[string]string{"a": "1", "b": "2"})
}

// ListTags models no error, so no failed read drops a row: a denial warns once
// under sagemaker:ListTags, and a throttle or any other code is returned. The
// sibling keeps its tags either way.
func TestSageMakerStoreTagged_ReadFailures(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		warns    int
		wantCode string
	}{
		{name: "denied", err: apiErr("AccessDeniedException", "denied"), warns: 1},
		{name: "transient", err: apiErr("ThrottlingException", "Rate exceeded"), wantCode: "ThrottlingException"},
		{name: "not found", err: apiErr("ResourceNotFound", "gone"), wantCode: "ResourceNotFound"},
		{name: "other error", err: apiErr("ValidationException", "bad"), wantCode: "ValidationException"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			bad, ok := smTestARN("model", "bad"), smTestARN("model", "ok")
			tags := newSMTags(smTagged, bad, ok)
			tags.errs = map[string]error{bad: c.err}
			total, _, err := sagemakerStoreTagged(context.Background(), tags, newTestAccount(testAccountID), testRegion, st, "sagemaker:ListModels")(smTagRows(bad, ok), nil, nil)
			if c.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertAPICode(t, err, c.wantCode)
			}
			if total != 2 {
				t.Errorf("total = %d; want 2", total)
			}
			rows := rowsByNativeID(t, st, TypeSageMakerModel)
			r, stored := rows[bad]
			if !stored {
				t.Fatal("row with the failed tag read not stored")
			}
			if r.TagsJSON != nil {
				t.Errorf("failed row TagsJSON = %s; want nil", *r.TagsJSON)
			}
			assertTagsMap(t, rows[ok].TagsJSON, map[string]string{"owner": ok})
			var got []store.ScanWarning
			for _, w := range *warns {
				if w.Service == sagemakerListTagsOp {
					got = append(got, w)
				}
			}
			if len(got) != c.warns {
				t.Fatalf("%s warnings = %v; want %d", sagemakerListTagsOp, got, c.warns)
			}
			if c.warns > 0 && got[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning scope = %q; want %q", got[0].Scope, testAccountID+"/"+testRegion)
			}
		})
	}
}

// smWarnServices counts warns by Service.
func smWarnServices(warns []store.ScanWarning) map[string]int {
	n := map[string]int{}
	for _, w := range warns {
		n[w.Service]++
	}
	return n
}

// A throttled tag read keeps its sagemaker:ListTags label through the phase
// and family runners: beside another family's hard error it is demoted to a
// warning under sagemaker:ListTags, not under its phase's List op or the
// service.
func TestSageMakerTagErrors_KeepListTagsOpThroughRunners(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	acct := newTestAccount(testAccountID)
	slow := smTestARN("model", "slow")
	tags := newSMTags(smUntagged)
	tags.errs = map[string]error{slow: apiErr("ThrottlingException", "Rate exceeded")}
	_, _, err := runSageMakerFamilies(context.Background(), st, acct, testRegion,
		func(ctx context.Context) (int, int, error) {
			return runSageMakerPhases(sagemakerPhase{"sagemaker:ListModels", func() (int, int, error) {
				return sagemakerStoreTagged(ctx, tags, acct, testRegion, st, "sagemaker:ListModels")(smTagRows(slow), nil, nil)
			}})
		},
		func(context.Context) (int, int, error) { return 0, 0, apiErr("ValidationException", "bad") },
	)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v; want outcomeError", err, got)
	}
	if got, want := smWarnServices(*warns), map[string]int{sagemakerListTagsOp: 1}; !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
}

// A phase's throttle beside a hard error in a sibling phase is demoted under
// the throttled phase's op, which only an unjoined phase result carries up.
func TestRunSageMakerPhases_ThrottleBesideHardPhaseDemotedUnderItsOp(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	_, _, err := runSageMakerFamilies(context.Background(), st, newTestAccount(testAccountID), testRegion,
		func(context.Context) (int, int, error) {
			return runSageMakerPhases(
				sagemakerPhase{"sagemaker:ListModels", func() (int, int, error) { return 0, 0, apiErr("ThrottlingException", "Rate exceeded") }},
				sagemakerPhase{"sagemaker:ListEndpoints", func() (int, int, error) { return 0, 0, apiErr("ValidationException", "bad") }},
			)
		},
	)
	assertAPICode(t, err, "ValidationException")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v; want outcomeError", err, got)
	}
	if got, want := smWarnServices(*warns), map[string]int{"sagemaker:ListModels": 1}; !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
}

// smGatedTags answers ListTags with no tags. The first fanoutMed reads wait
// until fanoutMed are in flight, so the reads overlap as far as the bound
// lets them; each read checks, on entry, that every read in flight holds one
// of slots.
type smGatedTags struct {
	slots    chan struct{}
	full     chan struct{}
	once     sync.Once
	inFlight atomic.Int32
	calls    atomic.Int32

	mu         sync.Mutex
	violations []string
}

func (s *smGatedTags) tagReadSlots() chan struct{} { return s.slots }

func (s *smGatedTags) ListTags(_ context.Context, _ *sagemaker.ListTagsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListTagsOutput, error) {
	s.calls.Add(1)
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	if held := len(s.slots); int(n) > held {
		s.mu.Lock()
		s.violations = append(s.violations, fmt.Sprintf("%d reads in flight, %d slots held", n, held))
		s.mu.Unlock()
	}
	if n == fanoutMed {
		s.once.Do(func() { close(s.full) })
	}
	<-s.full
	return &sagemaker.ListTagsOutput{}, nil
}

// Two families' tag reads share the client's one set of fanoutMed slots, so
// together they never have more than fanoutMed reads in flight, though each
// family alone would allow fanoutMed (readRowTags).
func TestSageMakerTagReads_ShareOneBoundAcrossFamilies(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tags := &smGatedTags{slots: make(chan struct{}, fanoutMed), full: make(chan struct{})}
	family := func(kind string) func(context.Context) (int, int, error) {
		arns := make([]string, fanoutMed)
		for i := range arns {
			arns[i] = smTestARN(kind, fmt.Sprint(i))
		}
		return func(ctx context.Context) (int, int, error) {
			return sagemakerStoreTagged(ctx, tags, acct, testRegion, st, "sagemaker:ListModels")(smTagRows(arns...), nil, nil)
		}
	}
	if _, _, err := runSageMakerFamilies(context.Background(), st, acct, testRegion, family("model"), family("endpoint")); err != nil {
		t.Fatalf("err = %v", err)
	}
	if got := tags.calls.Load(); got != 2*fanoutMed {
		t.Errorf("ListTags calls = %d; want %d", got, 2*fanoutMed)
	}
	if len(tags.violations) > 0 {
		t.Errorf("reads not bounded by the shared slots: %v", tags.violations)
	}
}

func TestRunSageMakerPhases_FailedPhaseRunsLaterPhases(t *testing.T) {
	ran := false
	total, _, err := runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListModels", func() (int, int, error) { return 1, 1, apiErr("ValidationException", "bad") }},
		sagemakerPhase{"sagemaker:ListEndpoints", func() (int, int, error) {
			ran = true
			return 2, 2, nil
		}},
	)
	assertAPICode(t, err, "ValidationException")
	if !ran || total != 3 {
		t.Errorf("later phase ran %v, total %d; want true, 3", ran, total)
	}
}

func TestRunSageMakerPhases_FailedWriteStops(t *testing.T) {
	ran := false
	_, _, err := runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListModels", func() (int, int, error) { return 0, 0, fmt.Errorf("upsert: %w", store.ErrStoreWrite) }},
		sagemakerPhase{"sagemaker:ListEndpoints", func() (int, int, error) {
			ran = true
			return 0, 0, nil
		}},
	)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if ran {
		t.Error("phase after the failed write ran")
	}
}

// One family's error does not cancel another: the context the sibling ran on
// is still live after the runner returns, which a cancelling runner (errgroup)
// would have ended by then.
func TestRunSageMakerFamilies_ErrorDoesNotCancelSiblings(t *testing.T) {
	st := newTestStore(t)
	var sibling context.Context
	total, _, err := runSageMakerFamilies(context.Background(), st, newTestAccount(testAccountID), testRegion,
		func(context.Context) (int, int, error) { return 1, 1, apiErr("ValidationException", "bad") },
		func(ctx context.Context) (int, int, error) {
			sibling = ctx
			return 2, 2, nil
		},
	)
	assertAPICode(t, err, "ValidationException")
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	if sibling.Err() != nil {
		t.Errorf("sibling family's context ended: %v", sibling.Err())
	}
}

// A family's throttle beside another family's hard error is demoted, so the
// hard error decides the outcome.
func TestRunSageMakerFamilies_TransientDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, sagemakerService)
	_, _, err := runSageMakerFamilies(context.Background(), st, newTestAccount(testAccountID), testRegion,
		func(context.Context) (int, int, error) { return 0, 0, apiErr("ThrottlingException", "Rate exceeded") },
		func(context.Context) (int, int, error) { return 0, 0, apiErr("ValidationException", "bad") },
	)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v; want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", sagemakerService, *warns)
	}
}

// scanSageMaker runs its families without cancelling on error: the context
// the Studio family's tag read ran on is still live after the scan returns,
// though the training family failed.
func TestScanSageMaker_FamilyErrorDoesNotCancelOthers(t *testing.T) {
	st := newTestStore(t)
	domainARN := smTestARN("domain", "d-1")
	var (
		mu      sync.Mutex
		tagCtxs []context.Context
	)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(map[string]func(context.Context, any) (any, error){
		"ListDomains": func(context.Context, any) (any, error) {
			return &sagemaker.ListDomainsOutput{Domains: []smtypes.DomainDetails{{DomainId: sdkaws.String("d-1")}}}, nil
		},
		"DescribeDomain": func(context.Context, any) (any, error) {
			return &sagemaker.DescribeDomainOutput{DomainArn: sdkaws.String(domainARN), DomainName: sdkaws.String("d")}, nil
		},
		"ListNotebookInstances": func(context.Context, any) (any, error) {
			return nil, apiErr("ValidationException", "bad")
		},
		"ListTags": func(ctx context.Context, _ any) (any, error) {
			mu.Lock()
			tagCtxs = append(tagCtxs, ctx)
			mu.Unlock()
			return &sagemaker.ListTagsOutput{Tags: []smtypes.Tag{{Key: sdkaws.String("env"), Value: sdkaws.String("prod")}}}, nil
		},
	}, apiErr("AccessDeniedException", "op not stubbed"))

	_, _, err := scanSageMaker(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	if len(tagCtxs) != 1 {
		t.Fatalf("ListTags calls = %d; want 1", len(tagCtxs))
	}
	if err := tagCtxs[0].Err(); err != nil {
		t.Errorf("tag read's context ended: %v", err)
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeSageMakerDomain)[domainARN].TagsJSON, map[string]string{"env": "prod"})
}

// smKeyPages serves pages as smPage does; after the last page it fails with
// err when err is not nil.
func smKeyPages(pages [][]string, err error) func(context.Context, *string) ([]string, *string, error) {
	failAt := strconv.Itoa(len(pages))
	return func(_ context.Context, token *string) ([]string, *string, error) {
		if err != nil && sv(token) == failAt {
			return nil, nil, err
		}
		keys, next, perr := smPage(pages, token)
		if next == nil && err != nil {
			next = &failAt
		}
		return keys, next, perr
	}
}

// smModelDescribe is a sagemakerDescribe over model names: describe fails a
// key with errs[key], answers "noarn" without an ARN, and builds a model row
// otherwise.
func smModelDescribe(pages [][]string, listErr error, errs map[string]error) sagemakerDescribe[string] {
	return sagemakerDescribe[string]{
		listOp: "sagemaker:ListModels", describeOp: "sagemaker:DescribeModel", notFound: []string{"ResourceNotFound"},
		list: smKeyPages(pages, listErr),
		name: sagemakerKeyName,
		describe: func(_ context.Context, key string) (*store.Resource, error) {
			if err := errs[key]; err != nil {
				return nil, err
			}
			if key == "noarn" {
				return nil, nil
			}
			return smTagRows(smTestARN("model", key))[0], nil
		},
	}
}

// Each key's describe failure costs only that key's row: a deleted one is
// dropped silently, a denied, nameless or ARN-less one is warned about after
// the write, a throttled one is an error demoted under the describe op beside
// a hard one, and the hard one is returned.
func TestSageMakerDescribe_ClassifiesEachKey(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	acct := newTestAccount(testAccountID)
	d := smModelDescribe([][]string{{"ok", "gone", "denied", ""}, {"noarn", "slow", "bad"}}, nil, map[string]error{
		"gone":   apiErr("ResourceNotFound", "gone"),
		"denied": apiErr("AccessDeniedException", "denied"),
		"slow":   apiErr("ThrottlingException", "Rate exceeded"),
		"bad":    apiErr("ValidationException", "bad"),
	})
	total, _, err := runSageMakerFamilies(context.Background(), st, acct, testRegion, func(ctx context.Context) (int, int, error) {
		return d.scan(ctx, st, acct, testRegion, sagemakerStoreTagged(ctx, newSMTags(smUntagged), acct, testRegion, st, d.listOp))
	})
	assertAPICode(t, err, "ValidationException")
	if isAPIErrorCode(err, "ResourceNotFound", "AccessDeniedException", "ThrottlingException") {
		t.Errorf("err = %v; want only the hard describe error", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1", total)
	}
	if got, want := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeSageMakerModel))), []string{smTestARN("model", "ok")}; !slices.Equal(got, want) {
		t.Errorf("stored = %v; want %v", got, want)
	}
	if got, want := smWarnServices(*warns), map[string]int{"sagemaker:ListModels": 1, "sagemaker:DescribeModel": 3}; !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
}

// A failed page keeps the keys of the pages before it, which are described
// and stored; a denied page is warned about rather than returned.
func TestSageMakerDescribe_FailedPageKeepsEarlierPages(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
		warns    int
	}{
		{name: "hard", err: apiErr("ValidationException", "bad"), wantCode: "ValidationException"},
		{name: "denied", err: apiErr("AccessDeniedException", "denied"), warns: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "sagemaker:ListModels")
			acct := newTestAccount(testAccountID)
			d := smModelDescribe([][]string{{"a"}, {"b"}}, c.err, nil)
			total, _, err := d.scan(context.Background(), st, acct, testRegion, sagemakerStoreTagged(context.Background(), newSMTags(smUntagged), acct, testRegion, st, d.listOp))
			if c.wantCode != "" {
				assertAPICode(t, err, c.wantCode)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != 2 {
				t.Errorf("total = %d; want 2", total)
			}
			if *warns != c.warns {
				t.Errorf("sagemaker:ListModels warnings = %d; want %d", *warns, c.warns)
			}
		})
	}
}

// Every SageMaker listing asks for its model's page cap, the in-use probe's
// one-item ListDomains apart.
func TestScanSageMaker_ListsAtModelPageCap(t *testing.T) {
	ops := []string{
		"ListActions", "ListAIWorkloadConfigs", "ListAlgorithms", "ListAliases", "ListAppImageConfigs", "ListApps",
		"ListClusterNodes", "ListClusters", "ListClusterSchedulerConfigs", "ListCodeRepositories", "ListComputeQuotas",
		"ListContexts", "ListDataQualityJobDefinitions", "ListDeviceFleets", "ListDevices", "ListDomains",
		"ListEdgeDeploymentPlans", "ListEndpointConfigs", "ListEndpoints", "ListExperiments", "ListFeatureGroups",
		"ListFlowDefinitions", "ListHubContents", "ListHubs", "ListHumanTaskUis", "ListImages", "ListImageVersions",
		"ListInferenceComponents", "ListInferenceExperiments", "ListLineageGroups", "ListMlflowApps",
		"ListMlflowTrackingServers", "ListModelBiasJobDefinitions", "ListModelCards",
		"ListModelExplainabilityJobDefinitions", "ListModelPackageGroups", "ListModelPackages",
		"ListModelQualityJobDefinitions", "ListModels", "ListMonitoringAlerts", "ListMonitoringSchedules",
		"ListNotebookInstanceLifecycleConfigs", "ListNotebookInstances", "ListPartnerApps", "ListPipelines",
		"ListProcessingJobs", "ListProjects", "ListSpaces", "ListStudioLifecycleConfigs", "ListSubscribedWorkteams",
		"ListTrainingPlans", "ListTrials", "ListUltraServersByReservedCapacity", "ListUserProfiles", "ListWorkforces",
		"ListWorkteams",
	}
	// Parents for every child listing, so each one is asked too.
	parents := map[string]any{
		"ListDomains": &sagemaker.ListDomainsOutput{Domains: []smtypes.DomainDetails{{DomainId: sdkaws.String("d")}}},
		"ListImages":  &sagemaker.ListImagesOutput{Images: []smtypes.Image{{ImageName: sdkaws.String("img")}}},
		"DescribeImage": &sagemaker.DescribeImageOutput{
			ImageArn: sdkaws.String(smTestARN("image", "img")), ImageName: sdkaws.String("img"),
		},
		"ListHubs":     &sagemaker.ListHubsOutput{HubSummaries: []smtypes.HubInfo{{HubArn: sdkaws.String(smTestARN("hub", "h")), HubName: sdkaws.String("h")}}},
		"ListClusters": &sagemaker.ListClustersOutput{ClusterSummaries: []smtypes.ClusterSummary{{ClusterName: sdkaws.String("c")}}},
		"DescribeCluster": &sagemaker.DescribeClusterOutput{
			ClusterArn: sdkaws.String(smTestARN("cluster", "c")), ClusterName: sdkaws.String("c"),
		},
		"ListMonitoringSchedules": &sagemaker.ListMonitoringSchedulesOutput{
			MonitoringScheduleSummaries: []smtypes.MonitoringScheduleSummary{{MonitoringScheduleName: sdkaws.String("m")}},
		},
		"DescribeMonitoringSchedule": &sagemaker.DescribeMonitoringScheduleOutput{
			MonitoringScheduleArn: sdkaws.String(smTestARN("monitoring-schedule", "m")), MonitoringScheduleName: sdkaws.String("m"),
		},
		"ListTrainingPlans": &sagemaker.ListTrainingPlansOutput{TrainingPlanSummaries: []smtypes.TrainingPlanSummary{{
			TrainingPlanArn: sdkaws.String(smTestARN("training-plan", "p")), TrainingPlanName: sdkaws.String("p"),
			ReservedCapacitySummaries: []smtypes.ReservedCapacitySummary{{
				ReservedCapacityArn: sdkaws.String(smTestARN("reserved-capacity", "rc")), ReservedCapacityType: smtypes.ReservedCapacityTypeUltraserver,
			}},
		}}},
	}
	var (
		mu  sync.Mutex
		got = map[string][]int32{}
	)
	handlers := map[string]func(any) (any, error){}
	for op, out := range parents {
		handlers[op] = func(any) (any, error) { return out, nil }
	}
	clientType := reflect.TypeFor[*sagemaker.Client]()
	for _, op := range ops {
		m, ok := clientType.MethodByName(op)
		if !ok {
			t.Fatalf("sagemaker.Client has no %s", op)
		}
		outType := m.Type.Out(0).Elem()
		parent, hasParent := parents[op]
		handlers[op] = func(in any) (any, error) {
			limit, ok := reflect.ValueOf(in).Elem().FieldByName("MaxResults").Interface().(*int32)
			if !ok {
				return nil, fmt.Errorf("stub %s: input has no *int32 MaxResults", op)
			}
			mu.Lock()
			got[op] = append(got[op], sdkaws.ToInt32(limit))
			mu.Unlock()
			if hasParent {
				return parent, nil
			}
			return reflect.New(outType).Interface(), nil
		}
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, apiErr("AccessDeniedException", "op not stubbed"))
	if _, _, err := scanSageMaker(context.Background(), acct, testRegion, newTestStore(t), testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, op := range ops {
		limits := got[op]
		if op == "ListDomains" {
			if len(limits) == 0 || limits[0] != 1 {
				t.Errorf("ListDomains MaxResults = %v; want the probe's 1 first", limits)
				continue
			}
			limits = limits[1:]
		}
		if len(limits) == 0 || slices.ContainsFunc(limits, func(n int32) bool { return n != 100 }) {
			t.Errorf("%s MaxResults = %v; want only 100, at least once", op, limits)
		}
	}
}

// smLeaves returns the members of err that hold no join themselves (err
// alone when it holds none).
func smLeaves(err error) []error {
	members := joinedMembers(err)
	if len(members) == 0 {
		return []error{err}
	}
	var leaves []error
	for _, m := range members {
		leaves = append(leaves, smLeaves(m)...)
	}
	return leaves
}

// A key whose describe is denied or still transient is stored from its
// listing when the listing carries its ARN, with its listed summary as the
// attributes and its tags read; the transient one is still returned, as its
// describe data is lost. Without listed, or when the listing has no ARN, the
// key is not stored, and a key deleted since its listing never is.
func TestSageMakerDescribe_DeniedOrTransientKeptFromListing(t *testing.T) {
	for _, withListed := range []bool{true, false} {
		t.Run(fmt.Sprintf("listed=%v", withListed), func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			acct := newTestAccount(testAccountID)
			d := smModelDescribe([][]string{{"ok", "denied", "slow", "nolistarn", "gone"}}, nil, map[string]error{
				"gone":      apiErr("ResourceNotFound", "gone"),
				"denied":    apiErr("AccessDeniedException", "denied"),
				"slow":      apiErr("ThrottlingException", "Rate exceeded"),
				"nolistarn": apiErr("AccessDeniedException", "denied"),
			})
			summary := func(key string) smtypes.ModelSummary {
				s := smtypes.ModelSummary{ModelName: sdkaws.String(key)}
				if key != "nolistarn" {
					s.ModelArn = sdkaws.String(smTestARN("model", key))
				}
				return s
			}
			if withListed {
				d.listed = func(key string) *store.Resource {
					s := summary(key)
					return sagemakerListedRow(acct, testRegion, testScanID, TypeSageMakerModel, s.ModelArn, s.ModelName, nil, "", s)
				}
			}
			tags := newSMTags(smUntagged)
			_, _, err := d.scan(context.Background(), st, acct, testRegion, sagemakerStoreTagged(context.Background(), tags, acct, testRegion, st, d.listOp))
			if err == nil || !everyMember(err, func(m error) bool { return isAPIErrorCode(m, "ThrottlingException") }) {
				t.Errorf("err = %v; want only the throttled describe", err)
			}
			want := []string{smTestARN("model", "ok")}
			wantWarns := map[string]int{"sagemaker:DescribeModel": 1}
			if withListed {
				want = append(want, smTestARN("model", "denied"), smTestARN("model", "slow"))
				wantWarns["sagemaker:DescribeModel"] = 2
			}
			slices.Sort(want)
			rows := rowsByNativeID(t, st, TypeSageMakerModel)
			if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, want) {
				t.Fatalf("stored = %v; want %v", got, want)
			}
			if got := tags.asked(); !slices.Equal(got, want) {
				t.Errorf("ListTags ARNs = %v; want %v", got, want)
			}
			if withListed {
				if got, want := rows[smTestARN("model", "denied")].AttributesJSON, mustJSON(summary("denied")); got != want {
					t.Errorf("denied row attributes = %s; want the listed summary %s", got, want)
				}
				if got := sv(rows[smTestARN("model", "slow")].Name); got != "slow" {
					t.Errorf("throttled row name = %q; want slow", got)
				}
			}
			if got := smWarnServices(*warns); !maps.Equal(got, wantWarns) {
				t.Errorf("warnings by op = %v; want %v", got, wantWarns)
			}
		})
	}
}

// An NXDOMAIN after the probe reached the endpoint, from a listing or a
// describe, is returned as a failure of this scan, not as a region gap the
// dispatcher would report while discarding the rows stored.
func TestSageMakerDescribe_LateNXDOMAINIsAnError(t *testing.T) {
	nxdomain := &net.DNSError{Err: "no such host", Name: "api.sagemaker.us-east-1.amazonaws.com", IsNotFound: true}
	cases := []struct {
		name     string
		listErr  error
		describe map[string]error
		tagged   bool
	}{
		{name: "describe", describe: map[string]error{"a": nxdomain}, tagged: true},
		{name: "tagged listing", listErr: nxdomain, tagged: true},
		{name: "untagged listing", listErr: nxdomain},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			d := smModelDescribe([][]string{{"a", "b"}}, c.listErr, c.describe)
			put := sagemakerStoreUntagged(st, acct, testRegion, d.listOp)
			if c.tagged {
				put = sagemakerStoreTagged(context.Background(), newSMTags(smUntagged), acct, testRegion, st, d.listOp)
			}
			_, _, err := d.scan(context.Background(), st, acct, testRegion, put)
			if err == nil || isDNSNotFound(err) {
				t.Errorf("err = %v; want a plain error, not an NXDOMAIN", err)
			}
			if _, ok := rowsByNativeID(t, st, TypeSageMakerModel)[smTestARN("model", "b")]; !ok {
				t.Errorf("model b not stored")
			}
		})
	}
}

// A probe NXDOMAIN is the region gap itself: returned as is, before any
// family runs.
func TestScanSageMaker_ProbeNXDOMAINIsReturned(t *testing.T) {
	nxdomain := &net.DNSError{Err: "no such host", Name: "api.sagemaker.us-east-1.amazonaws.com", IsNotFound: true}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListDomains": func(any) (any, error) { return nil, nxdomain },
	}, apiErr("AccessDeniedException", "op not stubbed"))
	if _, _, err := scanSageMaker(context.Background(), acct, testRegion, newTestStore(t), testScanID); !isDNSNotFound(err) {
		t.Errorf("err = %v; want the probe's NXDOMAIN", err)
	}
}

// Describes cut short by ctx are one error member, whether ctx ended only
// during the describes in flight or before some started, and every key cut
// short or never described is stored from its listing.
func TestSageMakerDescribe_StopIsOneMember(t *testing.T) {
	for _, n := range []int{3, fanoutMed + 2} {
		t.Run(strconv.Itoa(n)+" keys", func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			keys := make([]string, n)
			var want []string
			for i := range keys {
				keys[i] = strconv.Itoa(i)
				want = append(want, smTestARN("model", keys[i]))
			}
			slices.Sort(want)
			var started sync.WaitGroup
			started.Add(min(n, fanoutMed))
			d := smModelDescribe([][]string{keys}, nil, nil)
			d.describe = func(ctx context.Context, _ string) (*store.Resource, error) {
				started.Done()
				started.Wait()
				cancel()
				return nil, ctx.Err()
			}
			d.listed = func(key string) *store.Resource {
				return sagemakerListedRow(acct, testRegion, testScanID, TypeSageMakerModel, sdkaws.String(smTestARN("model", key)), &key, nil, "", smtypes.ModelSummary{})
			}
			_, _, err := d.describeEach(ctx, st, acct, testRegion, keys, nil, sagemakerStoreUntagged(st, acct, testRegion, d.listOp))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v; want context.Canceled", err)
			}
			if got := len(smLeaves(err)); got != 1 {
				t.Errorf("err has %d members; want 1: %v", got, err)
			}
			if got := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeSageMakerModel))); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want every listed key %v", got, want)
			}
		})
	}
}

// The production client gives every family the same tag-read bound.
func TestNewSageMakerClient_OneTagBound(t *testing.T) {
	c := newSageMakerClient(opStubConfig(nil, apiErr("AccessDeniedException", "op not stubbed")), testRegion)
	first, second := c.tagReadSlots(), c.tagReadSlots()
	if first != second {
		t.Error("tagReadSlots returned a new bound per call")
	}
	if got := cap(c.tagReadSlots()); got != fanoutMed {
		t.Errorf("tag read bound = %d; want fanoutMed (%d)", got, fanoutMed)
	}
}

// A key whose describe fails with a hard error is stored from its listing
// when the listing carries its ARN, and the error is still returned; without
// listed it is not stored.
func TestSageMakerDescribe_HardErrorKeptFromListing(t *testing.T) {
	for _, withListed := range []bool{true, false} {
		t.Run(fmt.Sprintf("listed=%v", withListed), func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			d := smModelDescribe([][]string{{"ok", "bad"}}, nil, map[string]error{"bad": apiErr("ValidationException", "bad")})
			if withListed {
				d.listed = func(key string) *store.Resource {
					return sagemakerListedRow(acct, testRegion, testScanID, TypeSageMakerModel, sdkaws.String(smTestARN("model", key)), &key, nil, "", smtypes.ModelSummary{})
				}
			}
			_, _, err := d.scan(context.Background(), st, acct, testRegion, sagemakerStoreUntagged(st, acct, testRegion, d.listOp))
			assertAPICode(t, err, "ValidationException")
			want := []string{smTestARN("model", "ok")}
			if withListed {
				want = append(want, smTestARN("model", "bad"))
			}
			slices.Sort(want)
			if got := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeSageMakerModel))); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
		})
	}
}

// smFilledOutput returns a new output of a List op whose list field holds one
// element, with every *string field set to "<op>/<field>" (so an ARN field
// names the op and field it was listed by), every string-kind (enum) field to
// its field name and every *int32 field to 1.
func smFilledOutput(op string, outType reflect.Type) any {
	out := reflect.New(outType)
	for i := range outType.NumField() {
		f := out.Elem().Field(i)
		if f.Kind() != reflect.Slice || f.Type().Elem().Kind() != reflect.Struct {
			continue
		}
		elem := reflect.New(f.Type().Elem()).Elem()
		for j := range elem.NumField() {
			field, name := elem.Field(j), elem.Type().Field(j).Name
			switch {
			case field.Type() == reflect.TypeFor[*string]():
				field.Set(reflect.ValueOf(sdkaws.String(op + "/" + name)))
			case field.Type() == reflect.TypeFor[*int32]():
				field.Set(reflect.ValueOf(sdkaws.Int32(1)))
			case field.Kind() == reflect.String:
				field.SetString(name)
			}
		}
		f.Set(reflect.Append(f, elem))
	}
	return out.Interface()
}

// With every Describe denied, each type whose listing carries its own ARN is
// still stored from its listing: under the ARN field of its summary, with the
// status field its describe stores ("" for none).
func TestScanSageMaker_DeniedDescribesKeepListedRows(t *testing.T) {
	type listedType struct{ op, arnField, statusField string }
	listedTypes := map[string]listedType{
		TypeSageMakerAppImageConfig:                   {"ListAppImageConfigs", "AppImageConfigArn", ""},
		TypeSageMakerCluster:                          {"ListClusters", "ClusterArn", "ClusterStatus"},
		TypeSageMakerCodeRepository:                   {"ListCodeRepositories", "CodeRepositoryArn", ""},
		TypeSageMakerDataQualityJobDefinition:         {"ListDataQualityJobDefinitions", "MonitoringJobDefinitionArn", ""},
		TypeSageMakerDevice:                           {"ListDevices", "DeviceArn", ""},
		TypeSageMakerDeviceFleet:                      {"ListDeviceFleets", "DeviceFleetArn", ""},
		TypeSageMakerDomain:                           {"ListDomains", "DomainArn", "Status"},
		TypeSageMakerEndpoint:                         {"ListEndpoints", "EndpointArn", "EndpointStatus"},
		TypeSageMakerEndpointConfig:                   {"ListEndpointConfigs", "EndpointConfigArn", ""},
		TypeSageMakerExperimentTrial:                  {"ListTrials", "TrialArn", ""},
		TypeSageMakerFeatureGroup:                     {"ListFeatureGroups", "FeatureGroupArn", "FeatureGroupStatus"},
		TypeSageMakerImage:                            {"ListImages", "ImageArn", "ImageStatus"},
		TypeSageMakerImageVersion:                     {"ListImageVersions", "ImageVersionArn", "ImageVersionStatus"},
		TypeSageMakerInferenceComponent:               {"ListInferenceComponents", "InferenceComponentArn", "InferenceComponentStatus"},
		TypeSageMakerMlflowTrackingServer:             {"ListMlflowTrackingServers", "TrackingServerArn", "TrackingServerStatus"},
		TypeSageMakerModel:                            {"ListModels", "ModelArn", ""},
		TypeSageMakerModelBiasJobDefinition:           {"ListModelBiasJobDefinitions", "MonitoringJobDefinitionArn", ""},
		TypeSageMakerModelCard:                        {"ListModelCards", "ModelCardArn", "ModelCardStatus"},
		TypeSageMakerModelExplainabilityJobDefinition: {"ListModelExplainabilityJobDefinitions", "MonitoringJobDefinitionArn", ""},
		TypeSageMakerModelPackage:                     {"ListModelPackages", "ModelPackageArn", "ModelPackageStatus"},
		TypeSageMakerModelPackageGroup:                {"ListModelPackageGroups", "ModelPackageGroupArn", "ModelPackageGroupStatus"},
		TypeSageMakerModelQualityJobDefinition:        {"ListModelQualityJobDefinitions", "MonitoringJobDefinitionArn", ""},
		TypeSageMakerMonitoringSchedule:               {"ListMonitoringSchedules", "MonitoringScheduleArn", "MonitoringScheduleStatus"},
		TypeSageMakerNotebookInstance:                 {"ListNotebookInstances", "NotebookInstanceArn", "NotebookInstanceStatus"},
		TypeSageMakerNotebookInstanceLifecycleConfig:  {"ListNotebookInstanceLifecycleConfigs", "NotebookInstanceLifecycleConfigArn", ""},
		TypeSageMakerPartnerApp:                       {"ListPartnerApps", "Arn", "Status"},
		TypeSageMakerPipeline:                         {"ListPipelines", "PipelineArn", ""},
		TypeSageMakerProcessingJob:                    {"ListProcessingJobs", "ProcessingJobArn", "ProcessingJobStatus"},
		TypeSageMakerProject:                          {"ListProjects", "ProjectArn", "ProjectStatus"},
		TypeSageMakerStudioLifecycleConfig:            {"ListStudioLifecycleConfigs", "StudioLifecycleConfigArn", ""},
		TypeSageMakerWorkteam:                         {"ListWorkteams", "WorkteamArn", ""},
	}
	handlers := map[string]func(any) (any, error){
		"ListTags": func(any) (any, error) { return &sagemaker.ListTagsOutput{}, nil },
	}
	clientType := reflect.TypeFor[*sagemaker.Client]()
	for i := range clientType.NumMethod() {
		m := clientType.Method(i)
		if !strings.HasPrefix(m.Name, "List") || m.Name == "ListTags" {
			continue
		}
		outType := m.Type.Out(0).Elem()
		handlers[m.Name] = func(in any) (any, error) {
			if tok := reflect.ValueOf(in).Elem().FieldByName("NextToken"); tok.IsValid() && !tok.IsNil() {
				return reflect.New(outType).Interface(), nil
			}
			return smFilledOutput(m.Name, outType), nil
		}
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, apiErr("AccessDeniedException", "op not stubbed"))
	st := newTestStore(t)
	if _, _, err := scanSageMaker(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for typ, want := range listedTypes {
		rows := rowsByNativeID(t, st, typ)
		if len(rows) != 1 {
			t.Errorf("%s rows = %d; want 1, stored from its listing", typ, len(rows))
			continue
		}
		for id, r := range rows {
			if wantID := want.op + "/" + want.arnField; id != wantID {
				t.Errorf("%s NativeID = %q; want %q", typ, id, wantID)
			}
			if got := sv(r.Status); got != want.statusField {
				t.Errorf("%s Status = %q; want %q", typ, got, want.statusField)
			}
		}
	}
}

// A probe that gets no answer from the endpoint (here a DNS server timeout)
// is returned before any family runs; one answered with an API error lets
// every family run.
func TestScanSageMaker_ProbeRunsFamiliesOnlyOnceAnswered(t *testing.T) {
	cases := []struct {
		name     string
		probeErr error
		families bool
	}{
		{name: "DNS timeout", probeErr: &net.DNSError{Err: "i/o timeout", Name: "api.sagemaker.us-east-1.amazonaws.com", IsTimeout: true}},
		{name: "API error", probeErr: apiErr("ThrottlingException", "Rate exceeded"), families: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var models atomic.Int32
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(map[string]func(any) (any, error){
				"ListDomains": func(any) (any, error) { return nil, c.probeErr },
				"ListModels": func(any) (any, error) {
					models.Add(1)
					return &sagemaker.ListModelsOutput{}, nil
				},
			}, apiErr("AccessDeniedException", "op not stubbed"))
			_, _, err := scanSageMaker(context.Background(), acct, testRegion, newTestStore(t), testScanID)
			if ran := models.Load() > 0; ran != c.families {
				t.Errorf("families ran = %v; want %v", ran, c.families)
			}
			if !c.families && !errors.Is(err, c.probeErr) {
				t.Errorf("err = %v; want the probe's error", err)
			}
		})
	}
}
