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
	"github.com/aws/aws-sdk-go-v2/service/kafkaconnect"
	kafkaconnecttypes "github.com/aws/aws-sdk-go-v2/service/kafkaconnect/types"
	"github.com/icearp/disco-cli/store"
)

// kcTestTotal is every row of the fixture: two of each of the three types,
// one per page.
const kcTestTotal = 2 * 3

var (
	kcTestDenied   = apiErr("ForbiddenException", "Access forbidden")
	kcTestThrottle = apiErr("TooManyRequestsException", "Rate exceeded")
	kcTestBad      = apiErr("BadRequestException", "bad")
	kcTestGone     = apiErr("NotFoundException", "gone")
	kcTestCreated  = time.Date(2025, 4, 2, 0, 0, 0, 0, time.UTC)
)

// kcTestKind is one listing: its op, the type it stores, its ARN's resource
// kind, the noun its warnings name its elements by, and the state its
// elements are listed in.
type kcTestKind struct {
	op, typ, kind, what, state string
}

func kcTestKinds() []kcTestKind {
	return []kcTestKind{
		{"ListConnectors", TypeKafkaConnectConnector, "connector", "connectors", string(kafkaconnecttypes.ConnectorStateRunning)},
		{"ListCustomPlugins", TypeKafkaConnectCustomPlugin, "custom-plugin", "custom plugins", string(kafkaconnecttypes.CustomPluginStateActive)},
		{"ListWorkerConfigurations", TypeKafkaConnectWorkerConfiguration, "worker-configuration", "worker configurations", string(kafkaconnecttypes.WorkerConfigurationStateActive)},
	}
}

func kcTestKindOf(t *testing.T, op string) kcTestKind {
	t.Helper()
	for _, k := range kcTestKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no MSK Connect listing", op)
	return kcTestKind{}
}

func (k kcTestKind) name(suffix string) string { return "name-" + k.kind + "-" + suffix }

// arnIn is the ARN of k's element with suffix, in the Service Reference's
// ARNFormat: <kind>/<name>/<uuid>.
func (k kcTestKind) arnIn(partition, region, suffix string) string {
	return "arn:" + partition + ":kafkaconnect:" + region + ":" + testAccountID + ":" + k.kind + "/" + k.name(suffix) + "/uuid-" + suffix
}

func (k kcTestKind) arn(suffix string) string { return k.arnIn("aws", testRegion, suffix) }

// element is k's listed element with suffix, carrying arn (nil for an
// element listed without one).
func (k kcTestKind) element(arn *string, suffix string) any {
	name := sdkaws.String(k.name(suffix))
	switch k.op {
	case "ListConnectors":
		return kafkaconnecttypes.ConnectorSummary{
			ConnectorArn: arn, ConnectorName: name, ConnectorState: kafkaconnecttypes.ConnectorState(k.state),
			CreationTime: &kcTestCreated, CurrentVersion: sdkaws.String("v1"),
			ServiceExecutionRoleArn: sdkaws.String("arn:aws:iam::" + testAccountID + ":role/r-" + suffix),
		}
	case "ListCustomPlugins":
		return kafkaconnecttypes.CustomPluginSummary{
			CustomPluginArn: arn, Name: name, CustomPluginState: kafkaconnecttypes.CustomPluginState(k.state),
			CreationTime: &kcTestCreated, Description: sdkaws.String("plugin " + suffix),
		}
	default:
		return kafkaconnecttypes.WorkerConfigurationSummary{
			WorkerConfigurationArn: arn, Name: name, WorkerConfigurationState: kafkaconnecttypes.WorkerConfigurationState(k.state),
			CreationTime: &kcTestCreated, Description: sdkaws.String("config " + suffix),
		}
	}
}

// page is k's output holding element e, and next.
func (k kcTestKind) page(e any, next *string) any {
	switch e := e.(type) {
	case kafkaconnecttypes.ConnectorSummary:
		return &kafkaconnect.ListConnectorsOutput{NextToken: next, Connectors: []kafkaconnecttypes.ConnectorSummary{e}}
	case kafkaconnecttypes.CustomPluginSummary:
		return &kafkaconnect.ListCustomPluginsOutput{NextToken: next, CustomPlugins: []kafkaconnecttypes.CustomPluginSummary{e}}
	default:
		return &kafkaconnect.ListWorkerConfigurationsOutput{NextToken: next, WorkerConfigurations: []kafkaconnecttypes.WorkerConfigurationSummary{e.(kafkaconnecttypes.WorkerConfigurationSummary)}}
	}
}

// kcTestCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or tagErrs. partition and region are those of the ARNs the
// listings return.
type kcTestCalls struct {
	mu                sync.Mutex
	partition, region string
	calls             map[string]int
	maxResults        map[string][]*int32
	tagARNs           []string
	tags              map[string]map[string]string
	tagErrs           map[string]error
}

// newKCTestCalls tags every "-tagged" row with its type.
func newKCTestCalls() *kcTestCalls {
	return newKCTestCallsIn("aws", testRegion)
}

func newKCTestCallsIn(partition, region string) *kcTestCalls {
	c := &kcTestCalls{
		partition: partition, region: region,
		calls: map[string]int{}, maxResults: map[string][]*int32{},
		tags: map[string]map[string]string{}, tagErrs: map[string]error{},
	}
	for _, k := range kcTestKinds() {
		c.tags[k.arnIn(partition, region, "tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

// record notes op's call and the MaxResults a listing was sent.
func (c *kcTestCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[op]++
	if op != "ListTagsForResource" {
		c.maxResults[op] = append(c.maxResults[op], bacField[*int32](in, "MaxResults"))
	}
}

func (c *kcTestCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[op]
}

func (c *kcTestCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *kcTestCalls) listTags(in any) (any, error) {
	arn := sv(in.(*kafkaconnect.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	return &kafkaconnect.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// twoPages answers k's op with "-tagged" on the first page and "-bare" on
// the second.
func (c *kcTestCalls) twoPages(k kcTestKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(k.element(sdkaws.String(k.arnIn(c.partition, c.region, suffix)), suffix), next), nil
	}
}

// kcTestHandlers answers every op the scan calls from c.
func kcTestHandlers(c *kcTestCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range kcTestKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// kcTestScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func kcTestScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, kcTestDenied)
	return scanKafkaConnect(ctx, acct, testRegion, st, testScanID)
}

// kcAllTagARNs is the ARN of every row of the fixture, sorted.
func kcAllTagARNs() []string {
	var arns []string
	for _, k := range kcTestKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

func kcWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// kcJSONValue is v's JSON form decoded, to compare with a row's decoded
// attributes.
func kcJSONValue(t *testing.T, v any) map[string]any {
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

// Every listing stores both its pages, each row under its listed ARN, in the
// scanned account's name, named by its listed name, with its listed state as
// Status, no CreatedAt, not managed, its listed element as its attributes,
// and the tags its tag read returns (nil for none). ListTagsForResource is
// sent exactly the ARNs of every row; every listing call is sent MaxResults
// 100. No error, no warning.
func TestScanKafkaConnect_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newKCTestCalls()

	counted, upserts := countUpserts(st)
	total, err := kcTestScan(context.Background(), counted, kcTestHandlers(calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != kcTestTotal || inserted != kcTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, kcTestTotal, kcTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range kcTestKinds() {
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
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || sv(r.Status) != k.state || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %q, %q, %q, %v, %v; want %q, %q, %q, no CreatedAt, false",
					arn, sv(r.Name), sv(r.Region), sv(r.Status), r.CreatedAt, r.ManagedByProvider, k.name(suffix), testRegion, k.state)
			}
			if got, want := sv(r.AccountName), newTestAccount(testAccountID).Name; got != want {
				t.Errorf("%s AccountName = %q; want %q", arn, got, want)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			if listed := kcJSONValue(t, k.element(sdkaws.String(arn), suffix)); !reflect.DeepEqual(attrs, listed) {
				t.Errorf("%s attrs = %s; want its listed element %v", arn, r.AttributesJSON, listed)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if got := calls.maxResults[k.op]; len(got) != 2 || got[0] == nil || *got[0] != 100 || got[1] == nil || *got[1] != 100 {
			t.Errorf("%s MaxResults = %v; want two calls sent 100", k.op, got)
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, kcAllTagARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, kcAllTagARNs())
	}
}

// kcStoredWithTags reports whether the row under arn is stored with tags. It
// does not fail the test, so a listing's handler can call it.
func kcStoredWithTags(st *store.Store, arn string) bool {
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
	return err == nil && r != nil && r.TagsJSON != nil
}

// Every listing writes its first page, with its tags, before it reads the
// second.
func TestScanKafkaConnect_PagesWrittenAsRead(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	handlers := kcTestHandlers(newKCTestCalls())
	var (
		mu          sync.Mutex
		storedFirst = map[string]bool{}
	)
	for _, k := range kcTestKinds() {
		list := handlers[k.op]
		handlers[k.op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				mu.Lock()
				storedFirst[k.op] = kcStoredWithTags(st, k.arn("tagged"))
				mu.Unlock()
			}
			return list(in)
		}
	}

	if _, err := kcTestScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, k := range kcTestKinds() {
		if !storedFirst[k.op] {
			t.Errorf("%s: %s not stored with its tags when the second page was read", k.op, k.arn("tagged"))
		}
	}
}

// A rescan reports every stored row in its total, not only the rows the store
// newly wrote. On a first scan into an empty store every row is new, so only a
// rescan catches a total taken from the store's written count.
func TestScanKafkaConnect_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, err := kcTestScan(context.Background(), st, kcTestHandlers(newKCTestCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, err := kcTestScan(context.Background(), st, kcTestHandlers(newKCTestCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != kcTestTotal {
		t.Errorf("rescan total = %d; want %d", total, kcTestTotal)
	}
}

// Empty listings: each listed once, nothing stored, no tag read, no error,
// no warning.
func TestScanKafkaConnect_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newKCTestCalls()
	handlers := kcTestHandlers(calls)
	empty := map[string]any{
		"ListConnectors":           &kafkaconnect.ListConnectorsOutput{},
		"ListCustomPlugins":        &kafkaconnect.ListCustomPluginsOutput{},
		"ListWorkerConfigurations": &kafkaconnect.ListWorkerConfigurationsOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	counted, upserts := countUpserts(st)
	total, err := kcTestScan(context.Background(), counted, handlers)
	inserted := upserts()
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range kcTestKinds() {
		if n := calls.count(k.op); n != 1 {
			t.Errorf("%s called %d times; want 1", k.op, n)
		}
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource called %d times; want 0", n)
	}
}

// A failed tag read. NotFoundException is modeled, so a resource deleted
// since it was listed is dropped silently. A denial (ForbiddenException)
// keeps the row untagged and warns once under the tag op with the scan's
// scope; a throttle keeps it and is transient; any other error keeps it and
// is an error.
func TestScanKafkaConnect_TagReadFailures(t *testing.T) {
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
	for _, k := range kcTestKinds() {
		cases = append(cases,
			tagFailure{k.kind + "/not found", k.op, kcTestGone, -1, "", kcTestTotal - 1, false, false},
			tagFailure{k.kind + "/denied", k.op, kcTestDenied, -1, "", kcTestTotal, true, true},
			tagFailure{k.kind + "/transient", k.op, kcTestThrottle, outcomeTransient, "TooManyRequestsException", kcTestTotal, true, false},
			tagFailure{k.kind + "/other error", k.op, kcTestBad, outcomeError, "BadRequestException", kcTestTotal, true, false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newKCTestCalls()
			k := kcTestKindOf(t, c.op)
			arn := k.arn("tagged")
			calls.tagErrs[arn] = c.err

			total, err := kcTestScan(context.Background(), st, kcTestHandlers(calls))
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
				want = []string{"kafkaconnect:ListTagsForResource"}
			}
			if got := kcWarnOps(w); !slices.Equal(got, want) {
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

// Denied tag reads in every phase send one warning for the whole scan; the
// rows are kept.
func TestScanKafkaConnect_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newKCTestCalls()
	calls.tagErrs[kcTestKindOf(t, "ListConnectors").arn("bare")] = kcTestDenied
	calls.tagErrs[kcTestKindOf(t, "ListCustomPlugins").arn("tagged")] = kcTestDenied
	calls.tagErrs[kcTestKindOf(t, "ListWorkerConfigurations").arn("bare")] = kcTestDenied

	total, err := kcTestScan(context.Background(), st, kcTestHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != kcTestTotal {
		t.Errorf("total = %d; want %d", total, kcTestTotal)
	}
	if got := kcWarnOps(warns()); !slices.Equal(got, []string{"kafkaconnect:ListTagsForResource"}) {
		t.Errorf("warnings = %v; want one under kafkaconnect:ListTagsForResource", got)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanKafkaConnect_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "kafkaconnect:ListTagsForResource")
	calls := newKCTestCalls()
	calls.tagErrs[kcTestKindOf(t, "ListConnectors").arn("bare")] = kcTestThrottle
	calls.tagErrs[kcTestKindOf(t, "ListWorkerConfigurations").arn("tagged")] = kcTestBad

	_, err := kcTestScan(context.Background(), st, kcTestHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if *warns != 1 {
		t.Errorf("kafkaconnect:ListTagsForResource warnings = %d; want 1", *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phase's rows
// stay stored.
func TestScanKafkaConnect_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeKafkaConnectCustomPlugin)
	calls := newKCTestCalls()
	calls.tagErrs[kcTestKindOf(t, "ListCustomPlugins").arn("tagged")] = kcTestDenied

	total, err := kcTestScan(context.Background(), st, kcTestHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListWorkerConfigurations"); n != 0 {
		t.Errorf("ListWorkerConfigurations called %d times; want 0 after the failed write", n)
	}
	if total != 2 {
		t.Errorf("total = %d; want the 2 connectors", total)
	}
	if n := len(rowsByNativeID(t, st, TypeKafkaConnectConnector)); n != 2 {
		t.Errorf("connectors = %d; want 2", n)
	}
}

// A denied tag read is still warned about when a later phase fails: its row
// was stored, so the scan's error does not hide why it has no tags.
func TestScanKafkaConnect_TagDenialWarnedBesideLaterFailure(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		fail func(t *testing.T, st *store.Store, calls *kcTestCalls, handlers map[string]func(any) (any, error))
		want func(t *testing.T, err error)
	}{
		{"later listing page fails", func(t *testing.T, _ *store.Store, calls *kcTestCalls, handlers map[string]func(any) (any, error)) {
			list := handlers["ListCustomPlugins"]
			handlers["ListCustomPlugins"] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record("ListCustomPlugins", in)
					return nil, kcTestBad
				}
				return list(in)
			}
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "BadRequestException")
		}},
		{"later tag read fails", func(t *testing.T, _ *store.Store, calls *kcTestCalls, _ map[string]func(any) (any, error)) {
			calls.tagErrs[kcTestKindOf(t, "ListWorkerConfigurations").arn("tagged")] = kcTestBad
		}, func(t *testing.T, err error) {
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "BadRequestException")
		}},
		{"later write fails", func(t *testing.T, st *store.Store, _ *kcTestCalls, _ map[string]func(any) (any, error)) {
			refuseStoreWrites(t, st, TypeKafkaConnectCustomPlugin)
		}, func(t *testing.T, err error) {
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newKCTestCalls()
			denied := kcTestKindOf(t, "ListConnectors").arn("bare")
			calls.tagErrs[denied] = kcTestDenied
			handlers := kcTestHandlers(calls)
			c.fail(t, st, calls, handlers)

			_, err := kcTestScan(context.Background(), st, handlers)
			c.want(t, err)
			w := warns()
			if got := kcWarnOps(w); !slices.Equal(got, []string{"kafkaconnect:ListTagsForResource"}) {
				t.Fatalf("warnings = %v; want one under kafkaconnect:ListTagsForResource", w)
			}
			if x := w[0]; x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, "first "+denied) {
				t.Errorf("warning = %+v; want Scope %s/%s, keyed %s", x, testAccountID, testRegion, denied)
			}
			r, ok := rowsByNativeID(t, st, TypeKafkaConnectConnector)[denied]
			if !ok {
				t.Fatalf("%s missing", denied)
			}
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", denied, *r.TagsJSON)
			}
		})
	}
}

// A listing's failed later page keeps the page read before it, and its error
// does not stop the other phases. Every listing's first page denied is
// warned about under its op, and the other phases run too.
func TestScanKafkaConnect_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	type listFailure struct {
		name      string
		op        string
		err       error
		laterPage bool
		wantKept  int
		wantWarn  bool
	}
	var cases []listFailure
	for _, k := range kcTestKinds() {
		cases = append(cases,
			listFailure{"later page fails/" + k.op, k.op, kcTestBad, true, 1, false},
			listFailure{"denied/" + k.op, k.op, kcTestDenied, false, 0, true})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newKCTestCalls()
			handlers := kcTestHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if later := bacField[*string](in, "NextToken") != nil; later == c.laterPage {
					calls.record(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, err := kcTestScan(context.Background(), st, handlers)
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
				wantWarn = []string{"kafkaconnect:" + c.op}
			}
			if got := kcWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
			if want := kcTestTotal - 2 + c.wantKept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			for _, k := range kcTestKinds() {
				rows := rowsByNativeID(t, st, k.typ)
				if k.op != c.op {
					if len(rows) != 2 {
						t.Errorf("%s rows = %d; want 2, its phase run", k.typ, len(rows))
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
// the later phase runs.
func TestScanKafkaConnect_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newKCTestCalls()
	handlers := kcTestHandlers(calls)
	twoPages := handlers["ListCustomPlugins"]
	handlers["ListCustomPlugins"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*kafkaconnect.ListCustomPluginsOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, err := kcTestScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != kcTestTotal {
		t.Errorf("total = %d; want %d", total, kcTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeKafkaConnectCustomPlugin)); n != 2 {
		t.Errorf("custom plugins = %d; want 2", n)
	}
	if n := len(rowsByNativeID(t, st, TypeKafkaConnectWorkerConfiguration)); n != 2 {
		t.Errorf("worker configurations = %d; want 2", n)
	}
}

// An element listed without an ARN is not stored, not read, and warned about
// once under its listing with the scan's scope, calling the listing's
// elements by their noun and keyed by its name; the rest of the listing is
// stored.
func TestScanKafkaConnect_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	for _, k := range kcTestKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newKCTestCalls()
			handlers := kcTestHandlers(calls)
			twoPages := handlers[k.op]
			handlers[k.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(k.op, in)
					return k.page(k.element(nil, "noarn"), nil), nil
				}
				return twoPages(in)
			}

			total, err := kcTestScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := kcTestTotal - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if n := len(rowsByNativeID(t, st, k.typ)); n != 1 {
				t.Errorf("%s rows = %d; want the 1 with an ARN", k.typ, n)
			}
			want := slices.DeleteFunc(kcAllTagARNs(), func(a string) bool { return a == k.arn("bare") })
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			w := warns()
			if got := kcWarnOps(w); !slices.Equal(got, []string{"kafkaconnect:" + k.op}) {
				t.Fatalf("warnings = %v; want one under kafkaconnect:%s", w, k.op)
			}
			if x := w[0]; x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, "listed "+k.what+" have no ARN") ||
				!strings.HasSuffix(x.Message, "first "+k.name("noarn")) {
				t.Errorf("warning = %+v; want Scope %s/%s, naming %s, keyed %q", x, testAccountID, testRegion, k.what, k.name("noarn"))
			}
		})
	}
}

// In GovCloud and China the listed ARN of every row is its NativeID and
// reaches the tag op unchanged.
func TestScanKafkaConnect_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newKCTestCallsIn(c.partition, c.region)
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(kcTestHandlers(calls), kcTestDenied)
			if _, err := scanKafkaConnect(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			var want []string
			for _, k := range kcTestKinds() {
				want = append(want, k.arnIn(c.partition, c.region, "tagged"), k.arnIn(c.partition, c.region, "bare"))
			}
			slices.Sort(want)
			if got := calls.sortedTagARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			plugins := kcTestKindOf(t, "ListCustomPlugins")
			arn := plugins.arnIn(c.partition, c.region, "tagged")
			assertTagsMap(t, rowsByNativeID(t, st, TypeKafkaConnectCustomPlugin)[arn].TagsJSON, map[string]string{"type": TypeKafkaConnectCustomPlugin})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing; the later phase is not run, and the rows
// written before the stop stay stored.
func TestScanKafkaConnect_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newKCTestCalls()
	handlers := kcTestHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListCustomPlugins"]
	handlers["ListCustomPlugins"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, err := kcTestScan(ctx, st, handlers)
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
	if n := calls.count("ListWorkerConfigurations"); n != 0 {
		t.Errorf("ListWorkerConfigurations called %d times after ctx ended", n)
	}
	if n := len(rowsByNativeID(t, st, TypeKafkaConnectConnector)); n != 2 {
		t.Errorf("connectors = %d; want 2", n)
	}
	if plugins := rowsByNativeID(t, st, TypeKafkaConnectCustomPlugin); len(plugins) != 1 || !kcStoredWithTags(st, kcTestKindOf(t, "ListCustomPlugins").arn("tagged")) {
		t.Errorf("custom plugins = %v; want only the first page's, with its tags", plugins)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable
// and no later phase runs.
func TestScanKafkaConnect_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "kafkaconnect.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := kcTestHandlers(newKCTestCalls())
		handlers["ListWorkerConfigurations"] = func(any) (any, error) { return nil, nx }
		total, err := kcTestScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := kcTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newKCTestCalls()
		calls.tagErrs[kcTestKindOf(t, "ListConnectors").arn("tagged")] = nx
		_, err := kcTestScan(context.Background(), st, kcTestHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newKCTestCalls()
		handlers := map[string]func(in any) (any, error){}
		for _, k := range kcTestKinds() {
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return nil, nx
			}
		}
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(handlers, nx)
		_, err := scanKafkaConnect(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListCustomPlugins"); n != 0 {
			t.Errorf("ListCustomPlugins called %d times; want 0 once the region proved unavailable", n)
		}
	})
}

// kcRow stores an element with an ARN under it, named name, with state as
// its Status (none for an empty state) and the element as its attributes.
// One without an ARN is no row; its warning calls the elements what and
// keys it by name, or by noIdentifier without one.
func TestKCRow(t *testing.T) {
	t.Parallel()
	acct := newTestAccount(testAccountID)
	for _, k := range kcTestKinds() {
		arn := k.arn("x")
		element := k.element(&arn, "x")
		for _, c := range []struct {
			name         string
			arn, rowName *string
			state        string
			why, key     string
		}{
			{"ARN", &arn, sdkaws.String("n"), k.state, "", ""},
			{"ARN, no state", &arn, sdkaws.String("n"), "", "", ""},
			{"no ARN, named", nil, sdkaws.String("n"), k.state, k.what + " have no ARN", "n"},
			{"empty ARN, unnamed", sdkaws.String(""), nil, k.state, k.what + " have no ARN", noIdentifier},
		} {
			t.Run(k.kind+"/"+c.name, func(t *testing.T) {
				r, why, key := kcRow(acct, testRegion, testScanID, k.typ, k.what, c.arn, c.rowName, c.state, element)
				if why != c.why || key != c.key {
					t.Errorf("why, key = %q, %q; want %q, %q", why, key, c.why, c.key)
				}
				if c.why != "" {
					if r != nil {
						t.Errorf("row = %+v; want none", r)
					}
					return
				}
				if r == nil {
					t.Fatal("row = nil; want one")
				}
				if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != acct.Name || r.Type != k.typ || r.NativeID != arn ||
					sv(r.Name) != "n" || sv(r.Region) != testRegion || r.DiscoveredBy != testScanID {
					t.Errorf("row = %+v; want %s under %s named n in %s, found by %s", r, k.typ, arn, testRegion, testScanID)
				}
				if c.state == "" {
					if r.Status != nil {
						t.Errorf("Status = %q; want none", *r.Status)
					}
				} else if sv(r.Status) != c.state {
					t.Errorf("Status = %q; want %q", sv(r.Status), c.state)
				}
				if r.AttributesJSON != mustJSON(element) {
					t.Errorf("attrs = %s; want the element verbatim", r.AttributesJSON)
				}
			})
		}
	}
}
