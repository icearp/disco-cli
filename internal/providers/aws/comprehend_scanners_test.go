package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/comprehend"
	comprehendtypes "github.com/aws/aws-sdk-go-v2/service/comprehend/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// comprehendTestTotal is every row of the fixture: two of each of the six
// types, one per page.
const comprehendTestTotal = 2 * 6

var (
	comprehendTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: comprehend:List")
	comprehendTestThrottle = apiErr("TooManyRequestsException", "Rate exceeded")
	comprehendTestBad      = apiErr("InvalidRequestException", "bad")
	comprehendTestGone     = apiErr("ResourceNotFoundException", "gone")
	// The two answers of a region that does not serve a listing to this
	// account (isComprehendRegionGap).
	comprehendTestNotEnabled  = apiErr("NotAuthorizedException", "Your account is not authorized to make this call.")
	comprehendTestUnsupported = apiErr("InvalidRequestException", "UNSUPPORTED_OPERATION: this operation is not supported in this region")
	comprehendTestCreated     = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

// comprehendKind is one stored type: the listing it comes from, its ARN's
// resource segment, and its elements' status. ListEndpoints serves two.
type comprehendKind struct{ op, typ, resource, status string }

func comprehendKinds() []comprehendKind {
	return []comprehendKind{
		{"ListDocumentClassifiers", TypeComprehendDocumentClassifier, "document-classifier", "TRAINED"},
		{"ListEntityRecognizers", TypeComprehendEntityRecognizer, "entity-recognizer", "TRAINED"},
		{"ListEndpoints", TypeComprehendDocumentClassifierEndpoint, "document-classifier-endpoint", "IN_SERVICE"},
		{"ListEndpoints", TypeComprehendEntityRecognizerEndpoint, "entity-recognizer-endpoint", "IN_SERVICE"},
		{"ListFlywheels", TypeComprehendFlywheel, "flywheel", "ACTIVE"},
		{"ListDatasets", TypeComprehendDataset, "dataset", "COMPLETED"},
	}
}

func comprehendKindOf(t *testing.T, typ string) comprehendKind {
	t.Helper()
	for _, k := range comprehendKinds() {
		if k.typ == typ {
			return k
		}
	}
	t.Fatalf("%s is no Comprehend type", typ)
	return comprehendKind{}
}

// comprehendFixture answers every op of a scan and records its calls. Each
// token listing returns its kinds' "-tagged" elements on its first page and
// "-bare" ones on its second; ListDatasets does the same for flywheel-tagged
// and returns no datasets for flywheel-bare. Every "-tagged" row is tagged
// with its type. Handlers run on fan-out goroutines, so mu guards the state
// they share.
type comprehendFixture struct {
	prefix string // every ARN's "arn:<partition>:comprehend:<region>:<account>:"

	mu               sync.Mutex
	calls            map[string]int
	maxResults       map[string][]int32
	tagARNs          []string
	datasetFlywheels []string
	tags             map[string]map[string]string
	errs             map[string]error // tag read errors by ARN
	noARN            map[string]bool  // types whose "-bare" element is listed without an ARN
	noStatus         bool             // every element listed with an empty status
}

func newComprehendFixture() *comprehendFixture {
	return newComprehendFixtureIn("aws", testRegion)
}

func newComprehendFixtureIn(partition, region string) *comprehendFixture {
	f := &comprehendFixture{
		prefix: "arn:" + partition + ":comprehend:" + region + ":" + testAccountID + ":",
		calls:  map[string]int{}, maxResults: map[string][]int32{},
		tags: map[string]map[string]string{}, errs: map[string]error{}, noARN: map[string]bool{},
	}
	for _, k := range comprehendKinds() {
		f.tags[f.arn(k, "tagged")] = map[string]string{"type": k.typ}
	}
	return f
}

// arn is the ARN of k's element with suffix: a model's names its version,
// a dataset's its flywheel (flywheel-tagged).
func (f *comprehendFixture) arn(k comprehendKind, suffix string) string {
	switch k.typ {
	case TypeComprehendDocumentClassifier, TypeComprehendEntityRecognizer:
		return f.prefix + k.resource + "/" + k.resource + "-" + suffix + "/version/v1"
	case TypeComprehendDataset:
		return f.prefix + "flywheel/flywheel-tagged/dataset/dataset-" + suffix
	}
	return f.prefix + k.resource + "/" + k.resource + "-" + suffix
}

func (f *comprehendFixture) flywheelARN(suffix string) string {
	return f.arn(comprehendKind{typ: TypeComprehendFlywheel, resource: "flywheel"}, suffix)
}

// allTagARNs is the ARN of every row of the fixture, sorted.
func (f *comprehendFixture) allTagARNs() []string {
	var arns []string
	for _, k := range comprehendKinds() {
		arns = append(arns, f.arn(k, "tagged"), f.arn(k, "bare"))
	}
	slices.Sort(arns)
	return arns
}

// element is k's listed element with suffix.
func (f *comprehendFixture) element(k comprehendKind, suffix string) any {
	arn := sdkaws.String(f.arn(k, suffix))
	if suffix == "bare" && f.noARN[k.typ] {
		arn = nil
	}
	status := k.status
	if f.noStatus {
		status = ""
	}
	switch k.typ {
	case TypeComprehendDocumentClassifier:
		return comprehendtypes.DocumentClassifierProperties{DocumentClassifierArn: arn, Status: comprehendtypes.ModelStatus(status), VersionName: sdkaws.String("v1"), LanguageCode: comprehendtypes.LanguageCodeEn}
	case TypeComprehendEntityRecognizer:
		return comprehendtypes.EntityRecognizerProperties{EntityRecognizerArn: arn, Status: comprehendtypes.ModelStatus(status), VersionName: sdkaws.String("v1"), LanguageCode: comprehendtypes.LanguageCodeEn}
	case TypeComprehendDocumentClassifierEndpoint, TypeComprehendEntityRecognizerEndpoint:
		model := comprehendKinds()[0]
		if k.typ == TypeComprehendEntityRecognizerEndpoint {
			model = comprehendKinds()[1]
		}
		return comprehendtypes.EndpointProperties{EndpointArn: arn, Status: comprehendtypes.EndpointStatus(status), ModelArn: sdkaws.String(f.arn(model, suffix)), DesiredInferenceUnits: sdkaws.Int32(1)}
	case TypeComprehendFlywheel:
		return comprehendtypes.FlywheelSummary{FlywheelArn: arn, Status: comprehendtypes.FlywheelStatus(status), ModelType: comprehendtypes.ModelTypeDocumentClassifier}
	default:
		return comprehendtypes.DatasetProperties{DatasetArn: arn, DatasetName: sdkaws.String("dataset-" + suffix), DatasetType: comprehendtypes.DatasetTypeTrain, Status: comprehendtypes.DatasetStatus(status), CreationTime: &comprehendTestCreated}
	}
}

// output is op's response listing the elements of suffix, then next.
func (f *comprehendFixture) output(op, suffix string, next *string) any {
	var elems []any
	if suffix != "" {
		for _, k := range comprehendKinds() {
			if k.op == op {
				elems = append(elems, f.element(k, suffix))
			}
		}
	}
	switch op {
	case "ListDocumentClassifiers":
		return &comprehend.ListDocumentClassifiersOutput{NextToken: next, DocumentClassifierPropertiesList: comprehendElems[comprehendtypes.DocumentClassifierProperties](elems)}
	case "ListEntityRecognizers":
		return &comprehend.ListEntityRecognizersOutput{NextToken: next, EntityRecognizerPropertiesList: comprehendElems[comprehendtypes.EntityRecognizerProperties](elems)}
	case "ListEndpoints":
		return &comprehend.ListEndpointsOutput{NextToken: next, EndpointPropertiesList: comprehendElems[comprehendtypes.EndpointProperties](elems)}
	case "ListFlywheels":
		return &comprehend.ListFlywheelsOutput{NextToken: next, FlywheelSummaryList: comprehendElems[comprehendtypes.FlywheelSummary](elems)}
	default:
		return &comprehend.ListDatasetsOutput{NextToken: next, DatasetPropertiesList: comprehendElems[comprehendtypes.DatasetProperties](elems)}
	}
}

func comprehendElems[T any](elems []any) []T {
	out := make([]T, len(elems))
	for i, e := range elems {
		out[i] = e.(T)
	}
	return out
}

// record notes op's call and its MaxResults.
func (f *comprehendFixture) record(op string, in any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		f.maxResults[op] = append(f.maxResults[op], *n)
	}
}

func (f *comprehendFixture) count(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[op]
}

func (f *comprehendFixture) sortedTagARNs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.tagARNs))
}

func (f *comprehendFixture) sortedDatasetFlywheels() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.datasetFlywheels))
}

func (f *comprehendFixture) listTags(in any) (any, error) {
	arn := sv(in.(*comprehend.ListTagsForResourceInput).ResourceArn)
	f.record("ListTagsForResource", in)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tagARNs = append(f.tagARNs, arn)
	if err := f.errs[arn]; err != nil {
		return nil, err
	}
	var tags []comprehendtypes.Tag
	for k, v := range f.tags[arn] {
		tags = append(tags, comprehendtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &comprehend.ListTagsForResourceOutput{ResourceArn: &arn, Tags: tags}, nil
}

// twoPages answers op with its "-tagged" elements, then its "-bare" ones.
func (f *comprehendFixture) twoPages(op string) func(any) (any, error) {
	return func(in any) (any, error) {
		f.record(op, in)
		if bacField[*string](in, "NextToken") != nil {
			return f.output(op, "bare", nil), nil
		}
		return f.output(op, "tagged", sdkaws.String("p2")), nil
	}
}

func (f *comprehendFixture) listDatasets(in any) (any, error) {
	flywheel := sv(in.(*comprehend.ListDatasetsInput).FlywheelArn)
	f.mu.Lock()
	f.datasetFlywheels = append(f.datasetFlywheels, flywheel)
	f.mu.Unlock()
	if flywheel != f.flywheelARN("tagged") {
		f.record("ListDatasets", in)
		return f.output("ListDatasets", "", nil), nil
	}
	return f.twoPages("ListDatasets")(in)
}

// handlers answers every op the scan calls from f.
func (f *comprehendFixture) handlers() map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": f.listTags,
		"ListDatasets":        f.listDatasets,
	}
	for _, op := range []string{"ListDocumentClassifiers", "ListEntityRecognizers", "ListEndpoints", "ListFlywheels"} {
		h[op] = f.twoPages(op)
	}
	return h
}

// comprehendScan runs the scan's real entry point against handlers in
// region; every op they do not answer is denied.
func comprehendScan(ctx context.Context, st *store.Store, region string, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, comprehendTestDenied)
	return scanComprehend(ctx, acct, region, st, testScanID)
}

func comprehendWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every type stores both its rows, each under its listed ARN (a model's
// version ARN included). A model, an endpoint or a flywheel is named by its
// ARN, has its status as Status and no CreatedAt; a dataset is named by its
// DatasetName, has its CreationTime and is contained by its flywheel. Each
// row's attributes are its listed element and its tags those its tag read
// returns (nil for none). ListTagsForResource is sent exactly the ARNs of
// every row, ListDatasets the ARN of every flywheel stored, and every
// listing MaxResults 500. No error, no warning.
func TestScanComprehend_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newComprehendFixture()

	total, inserted, err := comprehendScan(context.Background(), st, testRegion, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != comprehendTestTotal || inserted != comprehendTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, comprehendTestTotal, comprehendTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range comprehendKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := f.arn(k, suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			wantName, wantCreated := arn, ""
			if k.typ == TypeComprehendDataset {
				wantName, wantCreated = "dataset-"+suffix, "2026-09-01T12:00:00Z"
			}
			if sv(r.Name) != wantName || sv(r.Region) != testRegion || sv(r.Status) != k.status || sv(r.CreatedAt) != wantCreated {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %q, %q; want %q, %q, %q, %q", arn, sv(r.Name), sv(r.Region), sv(r.Status), sv(r.CreatedAt), wantName, testRegion, k.status, wantCreated)
			}
			if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != "Test Account" || r.DiscoveredBy != testScanID {
				t.Errorf("%s Provider, AccountID, AccountName, DiscoveredBy = %q, %q, %q, %q; want %q, %q, %q, %q", arn, r.Provider, r.AccountID, sv(r.AccountName), r.DiscoveredBy, "aws", testAccountID, "Test Account", testScanID)
			}
			want, err := json.Marshal(f.element(k, suffix))
			if err != nil {
				t.Fatal(err)
			}
			var gotAttrs, wantAttrs any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &gotAttrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			if err := json.Unmarshal(want, &wantAttrs); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotAttrs, wantAttrs) {
				t.Errorf("%s attrs = %s; want its element %s", arn, r.AttributesJSON, want)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	for op, want := range map[string][]int32{
		"ListDocumentClassifiers": {500, 500},
		"ListEntityRecognizers":   {500, 500},
		"ListEndpoints":           {500, 500},
		"ListFlywheels":           {500, 500},
		"ListDatasets":            {500, 500, 500},
	} {
		if got := f.maxResults[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}
	if got := f.sortedTagARNs(); !slices.Equal(got, f.allTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, f.allTagARNs())
	}
	if got, want := f.sortedDatasetFlywheels(), []string{f.flywheelARN("bare"), f.flywheelARN("tagged"), f.flywheelARN("tagged")}; !slices.Equal(got, want) {
		t.Errorf("ListDatasets FlywheelArns = %v; want %v", got, want)
	}
	datasets := comprehendKindOf(t, TypeComprehendDataset)
	for _, suffix := range []string{"tagged", "bare"} {
		assertContainsNative(t, st, f.flywheelARN("tagged"), f.arn(datasets, suffix))
	}
	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, f.flywheelARN("bare")), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("flywheel-bare contains %v; want nothing", rels)
	}
}

// comprehendStoredWithTags reports whether the row of typ under arn is stored
// with tags. It does not fail the test, so a listing's handler can call it.
func comprehendStoredWithTags(st *store.Store, typ, arn string) bool {
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, Types: []string{typ}, Limit: util.AllResources})
	if err != nil {
		return false
	}
	for _, r := range rows {
		if r.NativeID == arn {
			return r.TagsJSON != nil
		}
	}
	return false
}

// Every account-wide listing writes its first page, with its tags, before it
// reads the second.
func TestScanComprehend_PagesWrittenAsRead(t *testing.T) {
	st := newTestStore(t)
	f := newComprehendFixture()
	handlers := f.handlers()
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range comprehendKinds() {
		if k.op == "ListDatasets" || k.typ == TypeComprehendEntityRecognizerEndpoint {
			continue
		}
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = comprehendStoredWithTags(st, k.typ, f.arn(k, "tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := comprehendScan(context.Background(), st, testRegion, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, op := range []string{"ListDocumentClassifiers", "ListEntityRecognizers", "ListEndpoints", "ListFlywheels"} {
		if !storedFirst[op] {
			t.Errorf("%s: first page not stored with its tags when the second page was read", op)
		}
	}
}

// An element listed with an empty status is stored with Status "", not nil,
// except a dataset, stored with none.
func TestScanComprehend_EmptyStatus(t *testing.T) {
	st := newTestStore(t)
	f := newComprehendFixture()
	f.noStatus = true

	if _, _, err := comprehendScan(context.Background(), st, testRegion, f.handlers()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range comprehendKinds() {
		r, ok := rowsByNativeID(t, st, k.typ)[f.arn(k, "tagged")]
		if !ok {
			t.Errorf("%s missing", f.arn(k, "tagged"))
			continue
		}
		if k.typ == TypeComprehendDataset {
			if r.Status != nil {
				t.Errorf("%s Status = %q; want nil", f.arn(k, "tagged"), *r.Status)
			}
		} else if r.Status == nil || *r.Status != "" {
			t.Errorf("%s Status = %v; want a pointer to \"\"", f.arn(k, "tagged"), r.Status)
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanComprehend_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := comprehendScan(context.Background(), st, testRegion, newComprehendFixture().handlers()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := comprehendScan(context.Background(), st, testRegion, newComprehendFixture().handlers())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != comprehendTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, comprehendTestTotal)
	}
}

// Empty listings: each account-wide listing called once, no dataset listing
// (no flywheel), no tag read, nothing stored, no error, no warning.
func TestScanComprehend_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newComprehendFixture()
	handlers := f.handlers()
	for _, op := range []string{"ListDocumentClassifiers", "ListEntityRecognizers", "ListEndpoints", "ListFlywheels"} {
		handlers[op] = func(in any) (any, error) {
			f.record(op, in)
			return f.output(op, "", nil), nil
		}
	}

	total, inserted, err := comprehendScan(context.Background(), st, testRegion, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for op, want := range map[string]int{"ListDocumentClassifiers": 1, "ListEntityRecognizers": 1, "ListEndpoints": 1, "ListFlywheels": 1, "ListDatasets": 0, "ListTagsForResource": 0} {
		if n := f.count(op); n != want {
			t.Errorf("%s called %d times; want %d", op, n, want)
		}
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently. A denial keeps the row
// untagged and warns once under the tag op with the scan's scope; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanComprehend_TagReadFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		typ       string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"document classifier/not found", TypeComprehendDocumentClassifier, comprehendTestGone, -1, "", comprehendTestTotal - 1, false, false},
		{"entity recognizer/denied", TypeComprehendEntityRecognizer, comprehendTestDenied, -1, "", comprehendTestTotal, true, true},
		{"document classifier endpoint/transient", TypeComprehendDocumentClassifierEndpoint, comprehendTestThrottle, outcomeTransient, "TooManyRequestsException", comprehendTestTotal, true, false},
		{"entity recognizer endpoint/other error", TypeComprehendEntityRecognizerEndpoint, comprehendTestBad, outcomeError, "InvalidRequestException", comprehendTestTotal, true, false},
		{"dataset/not found", TypeComprehendDataset, comprehendTestGone, -1, "", comprehendTestTotal - 1, false, false},
		{"dataset/denied", TypeComprehendDataset, comprehendTestDenied, -1, "", comprehendTestTotal, true, true},
		{"flywheel/other error", TypeComprehendFlywheel, comprehendTestBad, outcomeError, "InvalidRequestException", comprehendTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newComprehendFixture()
			k := comprehendKindOf(t, c.typ)
			arn := f.arn(k, "tagged")
			f.errs[arn] = c.err

			total, _, err := comprehendScan(context.Background(), st, testRegion, f.handlers())
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, k.typ)
			r, ok := rows[arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if _, ok := rows[f.arn(k, "bare")]; !ok {
				t.Errorf("sibling %s missing", f.arn(k, "bare"))
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{comprehendTagsOp}
			}
			if got := comprehendWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A flywheel whose tag read finds it deleted is not stored, so its datasets
// are not listed.
func TestScanComprehend_FlywheelGoneListsNoDatasets(t *testing.T) {
	st := newTestStore(t)
	f := newComprehendFixture()
	f.errs[f.flywheelARN("tagged")] = comprehendTestGone

	total, _, err := comprehendScan(context.Background(), st, testRegion, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := comprehendTestTotal - 3; total != want {
		t.Errorf("total = %d; want %d (the flywheel and its 2 datasets gone)", total, want)
	}
	if got, want := f.sortedDatasetFlywheels(), []string{f.flywheelARN("bare")}; !slices.Equal(got, want) {
		t.Errorf("ListDatasets FlywheelArns = %v; want %v", got, want)
	}
}

// Denied tag reads in three phases send one warning for the whole scan; the
// rows are kept.
func TestScanComprehend_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newComprehendFixture()
	f.errs[f.arn(comprehendKindOf(t, TypeComprehendDocumentClassifier), "bare")] = comprehendTestDenied
	f.errs[f.arn(comprehendKindOf(t, TypeComprehendFlywheel), "bare")] = comprehendTestDenied
	f.errs[f.arn(comprehendKindOf(t, TypeComprehendDataset), "tagged")] = comprehendTestDenied

	total, _, err := comprehendScan(context.Background(), st, testRegion, f.handlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != comprehendTestTotal {
		t.Errorf("total = %d; want %d", total, comprehendTestTotal)
	}
	if got := comprehendWarnOps(warns()); !slices.Equal(got, []string{comprehendTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, comprehendTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanComprehend_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, comprehendTagsOp)
	f := newComprehendFixture()
	f.errs[f.arn(comprehendKindOf(t, TypeComprehendEntityRecognizer), "bare")] = comprehendTestThrottle
	f.errs[f.arn(comprehendKindOf(t, TypeComprehendDataset), "tagged")] = comprehendTestBad

	_, _, err := comprehendScan(context.Background(), st, testRegion, f.handlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", comprehendTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanComprehend_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeComprehendEntityRecognizerEndpoint)
	f := newComprehendFixture()
	f.errs[f.arn(comprehendKindOf(t, TypeComprehendDocumentClassifierEndpoint), "tagged")] = comprehendTestDenied

	total, _, err := comprehendScan(context.Background(), st, testRegion, f.handlers())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListFlywheels", "ListDatasets"} {
		if n := f.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if total != 4 {
		t.Errorf("total = %d; want the 4 classifiers and recognizers", total)
	}
	for _, typ := range []string{TypeComprehendDocumentClassifier, TypeComprehendEntityRecognizer} {
		if n := len(rowsByNativeID(t, st, typ)); n != 2 {
			t.Errorf("%s rows = %d; want 2", typ, n)
		}
	}
}

// A failed write of the datasets is returned as a store write failure and
// sends no warning about the rows it lost.
func TestScanComprehend_FailedDatasetWrite(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeComprehendDataset)
	f := newComprehendFixture()
	f.errs[f.arn(comprehendKindOf(t, TypeComprehendDataset), "tagged")] = comprehendTestDenied

	_, _, err := comprehendScan(context.Background(), st, testRegion, f.handlers())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases. A denied listing is warned about
// under its op instead, and the later phases run too. A region gap read as
// an empty listing only on a first page: on a later page it is the listing's
// error (unsupported) or denial warning (not enabled), for an account-wide
// listing and for a flywheel's dataset listing alike.
func TestScanComprehend_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		name      string
		op        string
		err       error
		laterPage bool
		wantKept  int
	}{
		{"later page fails", "ListDocumentClassifiers", comprehendTestBad, true, 1},
		{"later page fails mid-scan", "ListEndpoints", comprehendTestBad, true, 1},
		{"denied", "ListEntityRecognizers", comprehendTestDenied, false, 0},
		{"later page unsupported", "ListDocumentClassifiers", comprehendTestUnsupported, true, 1},
		{"later page not enabled", "ListDocumentClassifiers", comprehendTestNotEnabled, true, 1},
		{"dataset later page unsupported", "ListDatasets", comprehendTestUnsupported, true, 1},
		{"dataset later page not enabled", "ListDatasets", comprehendTestNotEnabled, true, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newComprehendFixture()
			handlers := f.handlers()
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") != nil) == c.laterPage {
					f.record(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := comprehendScan(context.Background(), st, testRegion, handlers)
			var wantWarn []string
			if isAccessDenied(c.err) {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				wantWarn = []string{"comprehend:" + c.op}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "InvalidRequestException")
			}
			if got := comprehendWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			var kinds []comprehendKind
			for _, k := range comprehendKinds() {
				if k.op == c.op {
					kinds = append(kinds, k)
				}
			}
			if want := comprehendTestTotal - 2*len(kinds) + c.wantKept*len(kinds); total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, k := range kinds {
				rows := rowsByNativeID(t, st, k.typ)
				if _, ok := rows[f.arn(k, "tagged")]; ok != (c.wantKept == 1) {
					t.Errorf("%s stored = %v; want %v", f.arn(k, "tagged"), ok, c.wantKept == 1)
				}
				if _, ok := rows[f.arn(k, "bare")]; ok {
					t.Errorf("%s stored; its page failed", f.arn(k, "bare"))
				}
			}
			if c.op != "ListDatasets" {
				if n := len(rowsByNativeID(t, st, TypeComprehendDataset)); n != 2 {
					t.Errorf("datasets (the last phase) = %d; want 2", n)
				}
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages; a
// dataset listing's too.
func TestScanComprehend_RepeatedTokenKeepsPages(t *testing.T) {
	for _, c := range []struct{ op, typ string }{
		{"ListFlywheels", TypeComprehendFlywheel},
		{"ListDatasets", TypeComprehendDataset},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			f := newComprehendFixture()
			handlers := f.handlers()
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				out, err := list(in)
				if n := bacField[*string](out, "NextToken"); n != nil || bacField[*string](in, "NextToken") != nil {
					setComprehendNextToken(out, sdkaws.String("same"))
				}
				return out, err
			}

			total, _, err := comprehendScan(context.Background(), st, testRegion, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != comprehendTestTotal {
				t.Errorf("total = %d; want %d", total, comprehendTestTotal)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 2 {
				t.Errorf("%s rows = %d; want 2", c.typ, n)
			}
		})
	}
}

func setComprehendNextToken(out any, next *string) {
	switch o := out.(type) {
	case *comprehend.ListFlywheelsOutput:
		o.NextToken = next
	case *comprehend.ListDatasetsOutput:
		o.NextToken = next
	}
}

// An element listed without an ARN is not stored, not sent to the tag op,
// and warned about once under its listing; the rest of the listing is
// stored.
func TestScanComprehend_ElementWithoutARN(t *testing.T) {
	for _, k := range comprehendKinds() {
		t.Run(k.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newComprehendFixture()
			f.noARN[k.typ] = true

			total, _, err := comprehendScan(context.Background(), st, testRegion, f.handlers())
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := comprehendTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, n)
			}
			want := slices.DeleteFunc(f.allTagARNs(), func(a string) bool { return a == f.arn(k, "bare") })
			if got := f.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			if got := comprehendWarnOps(warns()); !slices.Equal(got, []string{"comprehend:" + k.op}) {
				t.Errorf("warnings = %v; want one under comprehend:%s", got, k.op)
			}
		})
	}
}

// A GovCloud or China ARN reaches the tag op unchanged and is the row's
// NativeID.
func TestScanComprehend_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			f := newComprehendFixtureIn(c.partition, c.region)
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(f.handlers(), comprehendTestDenied)
			if _, _, err := scanComprehend(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := f.sortedTagARNs(); !slices.Equal(got, f.allTagARNs()) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, f.allTagARNs())
			}
			for _, k := range comprehendKinds() {
				arn := f.arn(k, "tagged")
				assertTagsMap(t, rowsByNativeID(t, st, k.typ)[arn].TagsJSON, map[string]string{"type": k.typ})
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanComprehend_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	f := newComprehendFixture()
	handlers := f.handlers()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListEndpoints"]
	handlers["ListEndpoints"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := comprehendScan(ctx, st, testRegion, handlers)
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
	for _, op := range []string{"ListFlywheels", "ListDatasets"} {
		if n := f.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanComprehend_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "comprehend.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := newComprehendFixture().handlers()
		handlers["ListFlywheels"] = func(any) (any, error) { return nil, nx }
		total, _, err := comprehendScan(context.Background(), st, testRegion, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := comprehendTestTotal - 4; total != want {
			t.Errorf("total = %d; want the %d rows before the flywheels counted", total, want)
		}
	})
	t.Run("a dataset listing", func(t *testing.T) {
		st := newTestStore(t)
		handlers := newComprehendFixture().handlers()
		handlers["ListDatasets"] = func(any) (any, error) { return nil, nx }
		total, _, err := comprehendScan(context.Background(), st, testRegion, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := comprehendTestTotal - 2; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		f := newComprehendFixture()
		f.errs[f.arn(comprehendKindOf(t, TypeComprehendDocumentClassifier), "tagged")] = nx
		_, _, err := comprehendScan(context.Background(), st, testRegion, f.handlers())
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		f := newComprehendFixture()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range comprehendKinds() {
			handlers[k.op] = func(in any) (any, error) {
				f.record(k.op, in)
				return nil, nx
			}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		_, _, err := scanComprehend(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := f.count("ListEntityRecognizers"); n != 0 {
			t.Errorf("ListEntityRecognizers called %d times; want 0 once the region proved unavailable", n)
		}
	})
}

// Where the region does not serve a listing to this account (the custom-model
// surface not enabled, or the per-region feature gap), its first page reads
// as an empty listing: nothing of it stored, no warning, no error, and the
// other phases run. A real IAM denial of the same listing still warns under
// its op.
func TestScanComprehend_RegionGapIsSilent(t *testing.T) {
	realDeny := apiErr("NotAuthorizedException", "User: arn:aws:sts::123456789012:assumed-role/DiscoScanner/x is not authorized to perform: comprehend:List")
	for _, op := range []string{"ListDocumentClassifiers", "ListEntityRecognizers", "ListEndpoints", "ListFlywheels", "ListDatasets"} {
		for name, c := range map[string]struct {
			err  error
			warn bool
		}{
			"not enabled": {comprehendTestNotEnabled, false},
			"unsupported": {comprehendTestUnsupported, false},
			"real denial": {realDeny, true},
		} {
			t.Run(op+"/"+name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				f := newComprehendFixture()
				handlers := f.handlers()
				handlers[op] = func(in any) (any, error) {
					f.record(op, in)
					return nil, c.err
				}

				total, _, err := comprehendScan(context.Background(), st, testRegion, handlers)
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				lost := 0
				for _, k := range comprehendKinds() {
					if k.op == op || (op == "ListFlywheels" && k.typ == TypeComprehendDataset) {
						lost += 2
						if n := len(rowsByNativeID(t, st, k.typ)); n != 0 {
							t.Errorf("%s rows = %d; want 0", k.typ, n)
						}
					}
				}
				if want := comprehendTestTotal - lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				var want []string
				if c.warn {
					want = []string{"comprehend:" + op}
				}
				if got := comprehendWarnOps(warns()); !slices.Equal(got, want) {
					t.Errorf("warnings = %v; want %v", got, want)
				}
			})
		}
	}
}

// One flywheel's dataset listing failing costs the other flywheel none of
// its datasets. A flywheel deleted since it was listed is skipped silently
// when none of its listing was read, and with a warning when it was deleted
// mid-listing; a denial warns once under ListDatasets; any other error is
// returned after the datasets are stored.
func TestScanComprehend_DatasetListingFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		flywheel  string // the flywheel whose listing fails
		laterPage bool
		err       error
		outcome   serviceOutcome // -1: no error
		wantRows  int
		warn      bool
	}{
		{"deleted flywheel", "bare", false, comprehendTestGone, -1, 2, false},
		{"flywheel deleted mid-listing", "tagged", true, comprehendTestGone, -1, 1, true},
		{"denied flywheel", "bare", false, comprehendTestDenied, -1, 2, true},
		{"other error", "bare", false, comprehendTestBad, outcomeError, 2, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newComprehendFixture()
			handlers := f.handlers()
			failing := f.flywheelARN(c.flywheel)
			handlers["ListDatasets"] = func(in any) (any, error) {
				if sv(in.(*comprehend.ListDatasetsInput).FlywheelArn) == failing && (bacField[*string](in, "NextToken") != nil) == c.laterPage {
					f.record("ListDatasets", in)
					return nil, c.err
				}
				return f.listDatasets(in)
			}

			total, _, err := comprehendScan(context.Background(), st, testRegion, handlers)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, "InvalidRequestException")
			}
			if want := comprehendTestTotal - 2 + c.wantRows; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			datasets := comprehendKindOf(t, TypeComprehendDataset)
			if n := len(rowsByNativeID(t, st, TypeComprehendDataset)); n != c.wantRows {
				t.Errorf("dataset rows = %d; want %d", n, c.wantRows)
			}
			assertContainsNative(t, st, f.flywheelARN("tagged"), f.arn(datasets, "tagged"))
			var want []string
			if c.warn {
				want = []string{"comprehend:ListDatasets"}
			}
			if got := comprehendWarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

func TestIsComprehendNotEnabled(t *testing.T) {
	if !isComprehendNotEnabled(apiErr("NotAuthorizedException", "Your account is not authorized to make this call.")) {
		t.Error("not-subscribed message should match")
	}
	// Same code family, real per-action denial — must not match.
	if isComprehendNotEnabled(apiErr("NotAuthorizedException", "User: arn:... is not authorized to perform: comprehend:ListEndpoints")) {
		t.Error("real IAM denial must not match")
	}
	// Right message, wrong code.
	if isComprehendNotEnabled(apiErr("AccessDeniedException", "account is not authorized to make this call")) {
		t.Error("wrong code must not match")
	}
	if isComprehendNotEnabled(nil) {
		t.Error("nil must not match")
	}
}
