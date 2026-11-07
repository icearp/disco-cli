package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	transcribetypes "github.com/aws/aws-sdk-go-v2/service/transcribe/types"
	"github.com/icearp/disco-cli/store"
)

// trsTestTotal is every row of the fixture: two of each of the five types,
// one per page.
const trsTestTotal = 2 * 5

var (
	trsTestDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: transcribe:List")
	trsTestThrottle   = apiErr("ThrottlingException", "Rate exceeded")
	trsTestBad        = apiErr("BadRequestException", "bad")
	trsTestGone       = apiErr("NotFoundException", "gone")
	trsTestGap        = apiErr("BadRequestException", "Call Analytics isn't supported in this region.")
	trsTestNotOffered = apiErr("BadRequestException", "Your account isn't authorized to call this operation.")
	trsTestCreated    = time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
)

// trsTestKind is one listing: its op, the type it stores, its ARN's resource
// type, and the Status its rows carry (nil for none).
type trsTestKind struct {
	op, typ, kind string
	status        *string
}

func trsTestKinds() []trsTestKind {
	return []trsTestKind{
		{"ListCallAnalyticsCategories", TypeTranscribeCallAnalyticsCategory, "analytics-category", nil},
		{"ListLanguageModels", TypeTranscribeLanguageModel, "language-model", sdkaws.String("COMPLETED")},
		{"ListVocabularies", TypeTranscribeVocabulary, "vocabulary", sdkaws.String("READY")},
		{"ListVocabularyFilters", TypeTranscribeVocabularyFilter, "vocabulary-filter", nil},
		{"ListMedicalVocabularies", TypeTranscribeMedicalVocabulary, "medical-vocabulary", sdkaws.String("PENDING")},
	}
}

// trsTagReadKinds are the listings whose rows' tags are read with
// ListTagsForResource: every one but the categories, whose elements carry
// their tags.
func trsTagReadKinds() []trsTestKind {
	return slices.DeleteFunc(trsTestKinds(), func(k trsTestKind) bool { return k.op == "ListCallAnalyticsCategories" })
}

func trsTestKindOf(t *testing.T, op string) trsTestKind {
	t.Helper()
	for _, k := range trsTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Transcribe listing", op)
	return trsTestKind{}
}

func (k trsTestKind) name(suffix string) string { return k.kind + "-" + suffix }

func (k trsTestKind) arnIn(partition, region, suffix string) string {
	return "arn:" + partition + ":transcribe:" + region + ":" + testAccountID + ":" + k.kind + "/" + k.name(suffix)
}

func (k trsTestKind) arn(suffix string) string { return k.arnIn("aws", testRegion, suffix) }

// tags is the tags the fixture gives every "-tagged" row of k.
func (k trsTestKind) tags() map[string]string { return map[string]string{"type": k.typ} }

// element is k's listed element named name (nil for an element listed
// without one). A "-tagged" category carries its tags.
func (k trsTestKind) element(name *string, suffix string) any {
	switch k.op {
	case "ListCallAnalyticsCategories":
		c := transcribetypes.CategoryProperties{
			CategoryName: name, CreateTime: &trsTestCreated, InputType: transcribetypes.InputTypePostCall,
			Rules: []transcribetypes.Rule{&transcribetypes.RuleMemberNonTalkTimeFilter{Value: transcribetypes.NonTalkTimeFilter{Threshold: sdkaws.Int64(1000)}}},
		}
		if suffix == "tagged" {
			c.Tags = []transcribetypes.Tag{{Key: sdkaws.String("type"), Value: sdkaws.String(k.typ)}}
		}
		return c
	case "ListLanguageModels":
		return transcribetypes.LanguageModel{
			ModelName: name, ModelStatus: transcribetypes.ModelStatusCompleted, CreateTime: &trsTestCreated,
			LanguageCode: transcribetypes.CLMLanguageCodeEnUs, BaseModelName: transcribetypes.BaseModelNameWideBand,
		}
	case "ListVocabularies":
		return transcribetypes.VocabularyInfo{VocabularyName: name, VocabularyState: transcribetypes.VocabularyStateReady, LanguageCode: transcribetypes.LanguageCodeEnUs, LastModifiedTime: &trsTestCreated}
	case "ListVocabularyFilters":
		return transcribetypes.VocabularyFilterInfo{VocabularyFilterName: name, LanguageCode: transcribetypes.LanguageCodeEnUs, LastModifiedTime: &trsTestCreated}
	default:
		return transcribetypes.VocabularyInfo{VocabularyName: name, VocabularyState: transcribetypes.VocabularyStatePending, LanguageCode: transcribetypes.LanguageCodeEnUs, LastModifiedTime: &trsTestCreated}
	}
}

// page is k's output holding element e, and next.
func (k trsTestKind) page(e any, next *string) any {
	switch k.op {
	case "ListCallAnalyticsCategories":
		return &transcribe.ListCallAnalyticsCategoriesOutput{NextToken: next, Categories: []transcribetypes.CategoryProperties{e.(transcribetypes.CategoryProperties)}}
	case "ListLanguageModels":
		return &transcribe.ListLanguageModelsOutput{NextToken: next, Models: []transcribetypes.LanguageModel{e.(transcribetypes.LanguageModel)}}
	case "ListVocabularies":
		return &transcribe.ListVocabulariesOutput{NextToken: next, Vocabularies: []transcribetypes.VocabularyInfo{e.(transcribetypes.VocabularyInfo)}}
	case "ListVocabularyFilters":
		return &transcribe.ListVocabularyFiltersOutput{NextToken: next, VocabularyFilters: []transcribetypes.VocabularyFilterInfo{e.(transcribetypes.VocabularyFilterInfo)}}
	default:
		return &transcribe.ListMedicalVocabulariesOutput{NextToken: next, Vocabularies: []transcribetypes.VocabularyInfo{e.(transcribetypes.VocabularyInfo)}}
	}
}

// empty is k's output listing nothing.
func (k trsTestKind) empty() any {
	switch k.op {
	case "ListCallAnalyticsCategories":
		return &transcribe.ListCallAnalyticsCategoriesOutput{}
	case "ListLanguageModels":
		return &transcribe.ListLanguageModelsOutput{}
	case "ListVocabularies":
		return &transcribe.ListVocabulariesOutput{}
	case "ListVocabularyFilters":
		return &transcribe.ListVocabularyFiltersOutput{}
	default:
		return &transcribe.ListMedicalVocabulariesOutput{}
	}
}

// trsTestCalls records the calls of a scan and answers the tag reads, by
// ARN, with tags or tagErrs. partition and region are those of the scanned
// account.
type trsTestCalls struct {
	mu                sync.Mutex
	partition, region string
	calls             map[string]int
	maxResults        map[string][]*int32
	tagARNs           []string
	tags              map[string]map[string]string
	tagErrs           map[string]error
}

// newTRSTestCalls tags every "-tagged" row whose tags are read with its type.
func newTRSTestCalls() *trsTestCalls { return newTRSTestCallsIn("aws", testRegion) }

func newTRSTestCallsIn(partition, region string) *trsTestCalls {
	c := &trsTestCalls{
		partition: partition, region: region,
		calls: map[string]int{}, maxResults: map[string][]*int32{},
		tags: map[string]map[string]string{}, tagErrs: map[string]error{},
	}
	for _, k := range trsTagReadKinds() {
		c.tags[k.arnIn(partition, region, "tagged")] = k.tags()
	}
	return c
}

// record notes op's call and the MaxResults a listing was sent.
func (c *trsTestCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if op != "ListTagsForResource" {
		c.maxResults[op] = append(c.maxResults[op], bacField[*int32](in, "MaxResults"))
	}
}

func (c *trsTestCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *trsTestCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *trsTestCalls) listTags(in any) (any, error) {
	arn := sv(in.(*transcribe.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	var tags []transcribetypes.Tag
	for k, v := range c.tags[arn] {
		tags = append(tags, transcribetypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &transcribe.ListTagsForResourceOutput{Tags: tags}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *trsTestCalls) twoPages(k trsTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(k.element(sdkaws.String(k.name(suffix)), suffix), next), nil
	}
}

// failFirstPage answers k's first page with err, recording the call.
func (c *trsTestCalls) failFirstPage(k trsTestKind, err error) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		return nil, err
	}
}

// failLaterPage answers k's first page from handlers and its second with
// err.
func (c *trsTestCalls) failLaterPage(k trsTestKind, handlers map[string]func(any) (any, error), err error) func(any) (any, error) {
	first := handlers[k.op]
	return func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			c.record(k.op, in)
			return nil, err
		}
		return first(in)
	}
}

// trsTestHandlers answers every op the scan calls from c.
func trsTestHandlers(c *trsTestCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range trsTestKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// trsTestScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func trsTestScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, trsTestDenied)
	return scanTranscribe(ctx, acct, testRegion, st, testScanID)
}

// trsAllTagARNs is the ARN of every row whose tags are read, sorted.
func trsAllTagARNs() []string {
	var arns []string
	for _, k := range trsTagReadKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

func trsWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// trsJSONValue is v's JSON form decoded, to compare with a row's decoded
// attributes.
func trsJSONValue(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	return m
}

// Every listing stores both its pages, each row under the ARN built in the
// Service Reference's format from its name (a category's resource type is
// analytics-category), in the scanned account's name, named by its listed
// name, with its type's Status, no CreatedAt, not managed, its listed
// element as its attributes, and its tags (nil for none): a category's from
// its element, every other row's from its tag read. ListTagsForResource is
// sent exactly the ARNs of the rows of the four other types; every listing
// call is sent MaxResults 100. No error, no warning.
func TestScanTranscribe_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newTRSTestCalls()

	total, inserted, err := trsTestScan(context.Background(), st, trsTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != trsTestTotal || inserted != trsTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, trsTestTotal, trsTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range trsTestKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", k.typ, len(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || !reflect.DeepEqual(r.Status, k.status) || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %q, %q, %v, %v, %v; want %q, %q, %v, no CreatedAt, false",
					arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, k.name(suffix), testRegion, k.status)
			}
			if got, want := sv(r.AccountName), newTestAccount(testAccountID).Name; got != want {
				t.Errorf("%s AccountName = %q; want %q", arn, got, want)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			if listed := trsJSONValue(t, k.element(sdkaws.String(k.name(suffix)), suffix)); !reflect.DeepEqual(attrs, listed) {
				t.Errorf("%s attrs = %s; want its listed element %v", arn, r.AttributesJSON, listed)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, k.tags())
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if got := calls.maxResults[k.op]; len(got) != 2 || got[0] == nil || *got[0] != 100 || got[1] == nil || *got[1] != 100 {
			t.Errorf("%s MaxResults = %v; want two calls sent 100", k.op, got)
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, trsAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, trsAllTagARNs())
	}
}

// trsStoredWithTags reports whether the row under arn is stored with tags.
// It does not fail the test, so a listing's handler can call it.
func trsStoredWithTags(st *store.Store, arn string) bool {
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	return err == nil && r != nil && r.TagsJSON != nil
}

// Every listing whose tags are read writes its first page, with its tags,
// before it reads the second.
func TestScanTranscribe_PagesWrittenAsRead(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	handlers := trsTestHandlers(newTRSTestCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range trsTagReadKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = trsStoredWithTags(st, k.arn("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, _, err := trsTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range trsTagReadKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("tagged"))
		}
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanTranscribe_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := trsTestScan(context.Background(), st, trsTestHandlers(newTRSTestCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := trsTestScan(context.Background(), st, trsTestHandlers(newTRSTestCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != trsTestTotal {
		t.Errorf("rescan total = %d; want %d", total, trsTestTotal)
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanTranscribe_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newTRSTestCalls()
	handlers := trsTestHandlers(calls)
	for _, k := range trsTestKinds() {
		handlers[k.op] = func(in any) (any, error) {
			calls.record(k.op, in)
			return k.empty(), nil
		}
	}

	total, inserted, err := trsTestScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range trsTestKinds() {
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource called %d times; want 0", n)
	}
}

// A failed tag read. NotFoundException is modeled, so a resource deleted
// since it was listed is dropped silently. A denial keeps the row untagged
// and warns once under the tag op with the scan's scope; a throttle keeps it
// and is transient; any other error keeps it and is an error.
func TestScanTranscribe_TagReadFailures(t *testing.T) {
	t.Parallel()
	type tagFailure struct {
		name      string
		op        string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}
	var cases []tagFailure
	for _, k := range trsTagReadKinds() {
		cases = append(cases,
			tagFailure{k.kind + "/not found", k.op, trsTestGone, -1, "", trsTestTotal - 1, false, false},
			tagFailure{k.kind + "/denied", k.op, trsTestDenied, -1, "", trsTestTotal, true, true},
			tagFailure{k.kind + "/transient", k.op, trsTestThrottle, outcomeTransient, "ThrottlingException", trsTestTotal, true, false},
			tagFailure{k.kind + "/other error", k.op, trsTestBad, outcomeError, "BadRequestException", trsTestTotal, true, false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newTRSTestCalls()
			k := trsTestKindOf(t, c.op)
			arn := k.arn("tagged")
			calls.tagErrs[arn] = c.err

			total, _, err := trsTestScan(context.Background(), st, trsTestHandlers(calls))
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
			if _, ok := rows[k.arn("bare")]; !ok {
				t.Errorf("sibling %s missing", k.arn("bare"))
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{"transcribe:ListTagsForResource"}
			}
			if got := trsWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, "first "+arn) {
					t.Errorf("warning = %+v; want Scope %s/%s, keyed %s", x, testAccountID, testRegion, arn)
				}
			}
		})
	}
}

// Denied tag reads in three phases send one warning for the whole scan; the
// rows are kept.
func TestScanTranscribe_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newTRSTestCalls()
	calls.tagErrs[trsTestKindOf(t, "ListLanguageModels").arn("bare")] = trsTestDenied
	calls.tagErrs[trsTestKindOf(t, "ListVocabularyFilters").arn("tagged")] = trsTestDenied
	calls.tagErrs[trsTestKindOf(t, "ListMedicalVocabularies").arn("bare")] = trsTestDenied

	total, _, err := trsTestScan(context.Background(), st, trsTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != trsTestTotal {
		t.Errorf("total = %d; want %d", total, trsTestTotal)
	}
	if got := trsWarnOps(warns()); !slices.Equal(got, []string{"transcribe:ListTagsForResource"}) {
		t.Errorf("warnings = %v; want one under transcribe:ListTagsForResource", got)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanTranscribe_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "transcribe:ListTagsForResource")
	calls := newTRSTestCalls()
	calls.tagErrs[trsTestKindOf(t, "ListLanguageModels").arn("bare")] = trsTestThrottle
	calls.tagErrs[trsTestKindOf(t, "ListMedicalVocabularies").arn("tagged")] = trsTestBad

	_, _, err := trsTestScan(context.Background(), st, trsTestHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if *warns != 1 {
		t.Errorf("transcribe:ListTagsForResource warnings = %d; want 1", *warns)
	}
}

// A throttled listing beside a hard failure of another listing is demoted to
// one warning under the throttled listing's op, so the hard error decides
// the outcome.
func TestScanTranscribe_ListThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "transcribe:ListCallAnalyticsCategories")
	calls := newTRSTestCalls()
	handlers := trsTestHandlers(calls)
	categories, vocabs := trsTestKindOf(t, "ListCallAnalyticsCategories"), trsTestKindOf(t, "ListVocabularies")
	handlers[categories.op] = calls.failFirstPage(categories, trsTestThrottle)
	handlers[vocabs.op] = calls.failFirstPage(vocabs, trsTestBad)

	_, _, err := trsTestScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if *warns != 1 {
		t.Errorf("transcribe:ListCallAnalyticsCategories warnings = %d; want 1", *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanTranscribe_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeTranscribeVocabulary)
	calls := newTRSTestCalls()
	calls.tagErrs[trsTestKindOf(t, "ListVocabularies").arn("tagged")] = trsTestDenied

	total, _, err := trsTestScan(context.Background(), st, trsTestHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListVocabularyFilters", "ListMedicalVocabularies"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if total != 4 {
		t.Errorf("total = %d; want the 2 categories and 2 language models", total)
	}
	for _, typ := range []string{TypeTranscribeCallAnalyticsCategory, TypeTranscribeLanguageModel} {
		if n := len(rowsByNativeID(t, st, typ)); n != 2 {
			t.Errorf("%s rows = %d; want 2", typ, n)
		}
	}
}

// A denied tag read is still warned about when a later phase fails: its row
// was stored, so the scan's error does not hide why it has no tags.
func TestScanTranscribe_TagDenialWarnedBesideLaterFailure(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newTRSTestCalls()
	denied := trsTestKindOf(t, "ListLanguageModels").arn("bare")
	calls.tagErrs[denied] = trsTestDenied
	handlers := trsTestHandlers(calls)
	medical := trsTestKindOf(t, "ListMedicalVocabularies")
	handlers[medical.op] = calls.failLaterPage(medical, handlers, trsTestBad)

	_, _, err := trsTestScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	w := warns()
	if got := trsWarnOps(w); !slices.Equal(got, []string{"transcribe:ListTagsForResource"}) {
		t.Fatalf("warnings = %v; want one under transcribe:ListTagsForResource", w)
	}
	if x := w[0]; x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, "first "+denied) {
		t.Errorf("warning = %+v; want Scope %s/%s, keyed %s", x, testAccountID, testRegion, denied)
	}
}

// A listing's failed later page keeps the page read before it, and its error
// does not stop the other phases. Every listing's first page denied is
// warned about under its op, and the other phases run too.
func TestScanTranscribe_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	type listFailure struct {
		name     string
		op       string
		err      error
		later    bool
		wantKept int
		wantWarn bool
	}
	var cases []listFailure
	for _, k := range trsTestKinds() {
		cases = append(cases,
			listFailure{"later page fails/" + k.op, k.op, trsTestBad, true, 1, false},
			listFailure{"first page fails/" + k.op, k.op, trsTestBad, false, 0, false},
			listFailure{"denied/" + k.op, k.op, trsTestDenied, false, 0, true})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newTRSTestCalls()
			handlers := trsTestHandlers(calls)
			k := trsTestKindOf(t, c.op)
			if c.later {
				handlers[c.op] = calls.failLaterPage(k, handlers, c.err)
			} else {
				handlers[c.op] = calls.failFirstPage(k, c.err)
			}

			total, _, err := trsTestScan(context.Background(), st, handlers)
			if c.wantWarn {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "BadRequestException")
			}
			var wantWarn []string
			if c.wantWarn {
				wantWarn = []string{"transcribe:" + c.op}
			}
			if got := trsWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := trsTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, other := range trsTestKinds() {
				rows := rowsByNativeID(t, st, other.typ)
				if other.op != c.op {
					if len(rows) != 2 {
						t.Errorf("%s rows = %d; want 2, its phase run", other.typ, len(rows))
					}
					continue
				}
				if _, ok := rows[k.arn("tagged")]; ok != (c.wantKept == 1) {
					t.Errorf("%s stored = %v; want %v", k.arn("tagged"), ok, c.wantKept == 1)
				}
				if _, ok := rows[k.arn("bare")]; ok {
					t.Errorf("%s stored; its page failed", k.arn("bare"))
				}
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages, and
// the later phases run.
func TestScanTranscribe_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListCallAnalyticsCategories", "ListVocabularies"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newTRSTestCalls()
			handlers := trsTestHandlers(calls)
			k := trsTestKindOf(t, op)
			handlers[op] = func(in any) (any, error) {
				calls.record(op, in)
				suffix := "tagged"
				if bacField[*string](in, "NextToken") != nil {
					suffix = "bare"
				}
				return k.page(k.element(sdkaws.String(k.name(suffix)), suffix), sdkaws.String("same")), nil
			}

			total, _, err := trsTestScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != trsTestTotal {
				t.Errorf("total = %d; want %d", total, trsTestTotal)
			}
			if n := calls.count(op); n != 2 {
				t.Errorf("%s called %d times; want 2", op, n)
			}
			for _, other := range trsTestKinds() {
				if n := len(rowsByNativeID(t, st, other.typ)); n != 2 {
					t.Errorf("%s rows = %d; want 2", other.typ, n)
				}
			}
		})
	}
}

// An element listed without a name is not stored, not read, and warned
// about once under its listing with the scan's scope, keyed noIdentifier;
// the rest of the listing is stored.
func TestScanTranscribe_ElementWithoutName(t *testing.T) {
	t.Parallel()
	for _, k := range trsTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newTRSTestCalls()
			handlers := trsTestHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(k.op, in)
					return k.page(k.element(sdkaws.String(""), "bare"), nil), nil
				}
				return twoPages(in)
			}

			total, _, err := trsTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := trsTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with a name", k.typ, n)
			}
			want := slices.DeleteFunc(trsAllTagARNs(), func(a string) bool { return a == k.arn("bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			w := warns()
			if got := trsWarnOps(w); !slices.Equal(got, []string{"transcribe:" + k.op}) {
				t.Fatalf("warnings = %v; want one under transcribe:%s", w, k.op)
			}
			if x := w[0]; x.Scope != testAccountID+"/"+testRegion || !strings.HasSuffix(x.Message, "first "+noIdentifier) {
				t.Errorf("warning = %+v; want Scope %s/%s, keyed %q", x, testAccountID, testRegion, noIdentifier)
			}
		})
	}
}

// In GovCloud and China every row is stored under its partition's ARN, and
// that ARN reaches the tag op unchanged.
func TestScanTranscribe_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newTRSTestCallsIn(c.partition, c.region)
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(trsTestHandlers(calls), trsTestDenied)
			if _, _, err := scanTranscribe(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			var want []string
			for _, k := range trsTagReadKinds() {
				want = append(want, k.arnIn(c.partition, c.region, "tagged"), k.arnIn(c.partition, c.region, "bare"))
			}
			slices.Sort(want)
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			medical := trsTestKindOf(t, "ListMedicalVocabularies")
			assertTagsMap(t, rowsByNativeID(t, st, medical.typ)[medical.arnIn(c.partition, c.region, "tagged")].TagsJSON, medical.tags())
			categories := trsTestKindOf(t, "ListCallAnalyticsCategories")
			arn := categories.arnIn(c.partition, c.region, "tagged")
			r, ok := rowsByNativeID(t, st, categories.typ)[arn]
			if !ok {
				t.Fatalf("%s missing", arn)
			}
			assertTagsMap(t, r.TagsJSON, categories.tags())
		})
	}
}

// Once ctx ends, the stop is one member of the error, naming the phases not
// run, although the phase in flight saw it in its listing; the later phases
// are not run, and the rows written before the stop stay stored.
func TestScanTranscribe_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newTRSTestCalls()
	handlers := trsTestHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListVocabularies"]
	handlers["ListVocabularies"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := trsTestScan(ctx, st, handlers)
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
	// Vocabularies runs before vocabulary filters and medical vocabularies, so
	// neither started.
	for _, op := range []string{"ListVocabularyFilters", "ListMedicalVocabularies"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeTranscribeLanguageModel)); n != 2 {
		t.Errorf("language models = %d; want 2", n)
	}
	vocabs := trsTestKindOf(t, "ListVocabularies")
	if rows := rowsByNativeID(t, st, vocabs.typ); len(rows) != 1 || !trsStoredWithTags(st, vocabs.arn("tagged")) {
		t.Errorf("vocabularies = %v; want only the first page's, with its tags", rows)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanTranscribe_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "transcribe.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := trsTestHandlers(newTRSTestCalls())
		handlers["ListMedicalVocabularies"] = func(any) (any, error) { return nil, nx }
		total, _, err := trsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := trsTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newTRSTestCalls()
		calls.tagErrs[trsTestKindOf(t, "ListLanguageModels").arn("tagged")] = nx
		_, _, err := trsTestScan(context.Background(), st, trsTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newTRSTestCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range trsTestKinds() {
			handlers[k.op] = calls.failFirstPage(k, nx)
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		_, _, err := scanTranscribe(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListLanguageModels"); n != 0 {
			t.Errorf("ListLanguageModels called %d times; want 0 once the region proved unavailable", n)
		}
	})
}

// A first page answering that Call Analytics is not supported in the region
// is an empty listing: no error, no warning, and the other listings stored.
// The same answer on a later page is an error, the first page kept.
func TestScanTranscribe_FeatureGap(t *testing.T) {
	t.Parallel()
	categories := trsTestKindOf(t, "ListCallAnalyticsCategories")
	t.Run("first page", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newTRSTestCalls()
		handlers := trsTestHandlers(calls)
		handlers[categories.op] = calls.failFirstPage(categories, trsTestGap)

		total, _, err := trsTestScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if want := trsTestTotal - 2; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
	t.Run("later page", func(t *testing.T) {
		st := newTestStore(t)
		calls := newTRSTestCalls()
		handlers := trsTestHandlers(calls)
		handlers[categories.op] = calls.failLaterPage(categories, handlers, trsTestGap)

		total, _, err := trsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "BadRequestException")
		if want := trsTestTotal - 1; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
		if _, ok := rowsByNativeID(t, st, categories.typ)[categories.arn("tagged")]; !ok {
			t.Errorf("%s missing; its page was read", categories.arn("tagged"))
		}
	})
}

// A region that does not offer Transcribe to the account answers every op
// that it is not authorized. Answered by a listing before any listing listed
// an item or failed other than by a denial, the scan reports the region
// unavailable and runs no later listing; a denial warned about before stays.
// Answered after a listing listed items or failed other than by a denial, it
// is an error of its listing, and the rows and later phases are kept.
func TestScanTranscribe_NotOffered(t *testing.T) {
	t.Parallel()
	t.Run("first listing", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newTRSTestCalls()
		handlers := trsTestHandlers(calls)
		categories := trsTestKindOf(t, "ListCallAnalyticsCategories")
		handlers[categories.op] = calls.failFirstPage(categories, trsTestNotOffered)

		_, _, err := trsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		for _, k := range trsTestKinds()[1:] {
			if n := calls.count(k.op); n != 0 {
				t.Errorf("%s called %d times; want 0", k.op, n)
			}
		}
	})
	t.Run("after a denied and an empty listing", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newTRSTestCalls()
		handlers := trsTestHandlers(calls)
		categories, models, vocabs := trsTestKindOf(t, "ListCallAnalyticsCategories"), trsTestKindOf(t, "ListLanguageModels"), trsTestKindOf(t, "ListVocabularies")
		handlers[categories.op] = calls.failFirstPage(categories, trsTestDenied)
		handlers[models.op] = func(in any) (any, error) {
			calls.record(models.op, in)
			return models.empty(), nil
		}
		handlers[vocabs.op] = calls.failFirstPage(vocabs, trsTestNotOffered)

		_, _, err := trsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if got := trsWarnOps(warns()); !slices.Equal(got, []string{"transcribe:ListCallAnalyticsCategories"}) {
			t.Errorf("warnings = %v; want the categories denial", got)
		}
		for _, op := range []string{"ListVocabularyFilters", "ListMedicalVocabularies"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times; want 0", op, n)
			}
		}
	})
	t.Run("on a later page after an empty first page", func(t *testing.T) {
		st := newTestStore(t)
		calls := newTRSTestCalls()
		handlers := trsTestHandlers(calls)
		categories := trsTestKindOf(t, "ListCallAnalyticsCategories")
		handlers[categories.op] = func(in any) (any, error) {
			calls.record(categories.op, in)
			if bacField[*string](in, "NextToken") != nil {
				return nil, trsTestNotOffered
			}
			return &transcribe.ListCallAnalyticsCategoriesOutput{NextToken: sdkaws.String("p2")}, nil
		}

		total, _, err := trsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "BadRequestException")
		if want := trsTestTotal - 2; total != want {
			t.Errorf("total = %d; want %d, the later phases run", total, want)
		}
	})
	t.Run("after a listing failed other than by a denial", func(t *testing.T) {
		st := newTestStore(t)
		calls := newTRSTestCalls()
		handlers := trsTestHandlers(calls)
		categories, models := trsTestKindOf(t, "ListCallAnalyticsCategories"), trsTestKindOf(t, "ListLanguageModels")
		handlers[categories.op] = calls.failFirstPage(categories, trsTestBad)
		handlers[models.op] = calls.failFirstPage(models, trsTestNotOffered)

		total, _, err := trsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "BadRequestException")
		if want := trsTestTotal - 4; total != want {
			t.Errorf("total = %d; want %d, the later phases run", total, want)
		}
		for _, typ := range []string{TypeTranscribeVocabulary, TypeTranscribeVocabularyFilter, TypeTranscribeMedicalVocabulary} {
			if n := len(rowsByNativeID(t, st, typ)); n != 2 {
				t.Errorf("%s rows = %d; want 2, their phase run", typ, n)
			}
		}
	})
	t.Run("after a listing listed items", func(t *testing.T) {
		st := newTestStore(t)
		calls := newTRSTestCalls()
		handlers := trsTestHandlers(calls)
		vocabs := trsTestKindOf(t, "ListVocabularies")
		handlers[vocabs.op] = calls.failFirstPage(vocabs, trsTestNotOffered)

		total, _, err := trsTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "BadRequestException")
		if want := trsTestTotal - 2; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
		if n := len(rowsByNativeID(t, st, TypeTranscribeMedicalVocabulary)); n != 2 {
			t.Errorf("medical vocabularies = %d; want 2, their phase run", n)
		}
	})
}
