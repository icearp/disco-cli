package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/schemas"
	schemastypes "github.com/aws/aws-sdk-go-v2/service/schemas/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	esForbidden = &smithy.GenericAPIError{Code: "ForbiddenException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: schemas:List"}
	esBadReq    = &smithy.GenericAPIError{Code: "BadRequestException", Message: "bad"}
	esNotFound  = &smithy.GenericAPIError{Code: "NotFoundException", Message: "no policy"}
	esThrottled = apiErr("ThrottlingException", "Rate exceeded")
)

var esTags = map[string]string{"team": "es"}

func esARN(kind, path string) string {
	return fmt.Sprintf("arn:aws:schemas:%s:%s:%s/%s", testRegion, testAccountID, kind, path)
}

func esPolicyARN(registry string) string { return esARN("registry", registry) + "/policy" }

// esFixture is what the stub lists: each list op has two pages, the page-one
// element is tagged and the others are not, and each listing holds an element
// with no ARN. Registry "noname" has an ARN but no RegistryName. reg-a has a
// policy, reg-b has none (NotFoundException) and reg-c has an empty one.
type esFixture struct {
	discoverers [][]schemastypes.DiscovererSummary
	registries  [][]schemastypes.RegistrySummary
	schemas     map[string][][]schemastypes.SchemaSummary // by registry name
	policies    map[string]*schemas.GetResourcePolicyOutput
}

var esModified = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newESFixture() esFixture {
	reg := func(name string, tags map[string]string) schemastypes.RegistrySummary {
		return schemastypes.RegistrySummary{RegistryArn: ptrStr(esARN("registry", name)), RegistryName: ptrStr(name), Tags: tags}
	}
	schema := func(registry, name string, tags map[string]string) schemastypes.SchemaSummary {
		v := int64(2)
		return schemastypes.SchemaSummary{SchemaArn: ptrStr(esARN("schema", registry+"/"+name)), SchemaName: ptrStr(name), LastModified: &esModified, VersionCount: &v, Tags: tags}
	}
	return esFixture{
		discoverers: [][]schemastypes.DiscovererSummary{
			{{DiscovererArn: ptrStr(esARN("discoverer", "d-1")), DiscovererId: ptrStr("d-1"), SourceArn: ptrStr("arn:aws:events:us-east-1:123456789012:event-bus/default"), State: schemastypes.DiscovererStateStarted, Tags: esTags}},
			{{DiscovererArn: ptrStr(esARN("discoverer", "d-2")), DiscovererId: ptrStr("d-2")}, {DiscovererId: ptrStr("d-noarn")}},
		},
		registries: [][]schemastypes.RegistrySummary{
			{reg("reg-a", esTags)},
			{reg("reg-b", nil), reg("reg-c", nil), {RegistryName: ptrStr("reg-noarn")}, {RegistryArn: ptrStr(esARN("registry", "noname"))}},
		},
		schemas: map[string][][]schemastypes.SchemaSummary{
			"reg-a": {{schema("reg-a", "s1", esTags)}, {schema("reg-a", "s2", nil), {SchemaName: ptrStr("s-noarn")}}},
			"reg-b": {{schema("reg-b", "s3", nil)}},
			"reg-c": {{}},
		},
		policies: map[string]*schemas.GetResourcePolicyOutput{
			"reg-a": {Policy: ptrStr(`{"Version":"2012-10-17"}`), RevisionId: ptrStr("r1")},
			"reg-c": {Policy: ptrStr("")},
		},
	}
}

// esServe returns page token ("" or "p2") of pages, or fail[parent+"/"+token]
// when set.
func esServe[Item any](pages [][]Item, parent string, token *string, fail map[string]error) ([]Item, *string, error) {
	if err := fail[parent+"/"+sv(token)]; err != nil {
		return nil, nil, err
	}
	i := 0
	if sv(token) == "p2" {
		i = 1
	} else if token != nil {
		return nil, nil, fmt.Errorf("unexpected NextToken %q", sv(token))
	}
	if i >= len(pages) {
		return nil, nil, fmt.Errorf("parent %q has no page %d", parent, i+1)
	}
	var next *string
	if i+1 < len(pages) {
		next = ptrStr("p2")
	}
	return pages[i], next, nil
}

// esUnfiltered fails a call that sets a page size or a filter: either would
// change what the listing returns.
func esUnfiltered(limit *int32, filters ...*string) error {
	if limit != nil {
		return fmt.Errorf("Limit = %d, want unset", *limit)
	}
	for _, f := range filters {
		if f != nil {
			return fmt.Errorf("filter %q set, want none", *f)
		}
	}
	return nil
}

// esHandlers serves f; fail is keyed by op, then by "<parent>/<token>" (the
// parent is "" for the account-wide lists and the registry name otherwise;
// the token is "" on page one). calls records each per-registry call.
func esHandlers(f esFixture, fail map[string]map[string]error, calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListDiscoverers": func(in any) (any, error) {
			p := in.(*schemas.ListDiscoverersInput)
			if err := esUnfiltered(p.Limit, p.DiscovererIdPrefix, p.SourceArnPrefix); err != nil {
				return nil, err
			}
			items, next, err := esServe(f.discoverers, "", p.NextToken, fail["ListDiscoverers"])
			if err != nil {
				return nil, err
			}
			return &schemas.ListDiscoverersOutput{Discoverers: items, NextToken: next}, nil
		},
		"ListRegistries": func(in any) (any, error) {
			p := in.(*schemas.ListRegistriesInput)
			if err := esUnfiltered(p.Limit, p.RegistryNamePrefix, p.Scope); err != nil {
				return nil, err
			}
			items, next, err := esServe(f.registries, "", p.NextToken, fail["ListRegistries"])
			if err != nil {
				return nil, err
			}
			return &schemas.ListRegistriesOutput{Registries: items, NextToken: next}, nil
		},
		"ListSchemas": func(in any) (any, error) {
			p := in.(*schemas.ListSchemasInput)
			if err := esUnfiltered(p.Limit, p.SchemaNamePrefix); err != nil {
				return nil, err
			}
			name := sv(p.RegistryName)
			calls["ListSchemas"].add(name)
			items, next, err := esServe(f.schemas[name], name, p.NextToken, fail["ListSchemas"])
			if err != nil {
				return nil, err
			}
			return &schemas.ListSchemasOutput{Schemas: items, NextToken: next}, nil
		},
		"GetResourcePolicy": func(in any) (any, error) {
			name := sv(in.(*schemas.GetResourcePolicyInput).RegistryName)
			calls["GetResourcePolicy"].add(name)
			if err := fail["GetResourcePolicy"][name]; err != nil {
				return nil, err
			}
			out, ok := f.policies[name]
			if !ok {
				return nil, esNotFound
			}
			return out, nil
		},
	}
}

func newESCalls() map[string]*parentCalls {
	return map[string]*parentCalls{"ListSchemas": {}, "GetResourcePolicy": {}}
}

var esOpLabels = map[string]string{
	"ListDiscoverers":   "schemas:ListDiscoverers",
	"ListRegistries":    "schemas:ListRegistries",
	"ListSchemas":       "schemas:ListSchemas",
	"GetResourcePolicy": "schemas:GetResourcePolicy",
}

// esRun is what one scanEventSchemas run returned and warned.
type esRun struct {
	total, inserted int
	err             error
	warnings        []store.ScanWarning
}

// warns returns the warnings recorded under op (a key of esOpLabels).
func (r esRun) warns(op string) []store.ScanWarning {
	var out []store.ScanWarning
	for _, w := range r.warnings {
		if w.Service == esOpLabels[op] {
			out = append(out, w)
		}
	}
	return out
}

func scanESTopLevel(t *testing.T, st *store.Store, fail map[string]map[string]error, calls map[string]*parentCalls) esRun {
	t.Helper()
	var (
		mu  sync.Mutex
		run esRun
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		run.warnings = append(run.warnings, w)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(esHandlers(newESFixture(), fail, calls), esForbidden)
	run.total, run.inserted, run.err = scanEventSchemas(context.Background(), acct, testRegion, st, testScanID)
	return run
}

// esAllRows is every NativeID the full fixture stores, per type.
var esAllRows = map[string][]string{
	TypeEventSchemasDiscoverer:     {esARN("discoverer", "d-1"), esARN("discoverer", "d-2")},
	TypeEventSchemasRegistry:       {esARN("registry", "reg-a"), esARN("registry", "reg-b"), esARN("registry", "reg-c"), esARN("registry", "noname")},
	TypeEventSchemasSchema:         {esARN("schema", "reg-a/s1"), esARN("schema", "reg-a/s2"), esARN("schema", "reg-b/s3")},
	TypeEventSchemasRegistryPolicy: {esPolicyARN("reg-a")},
}

// esWithout is esAllRows with types typs, and the given rows, removed.
func esWithout(typs []string, rows ...string) map[string][]string {
	out := map[string][]string{}
	for typ, ids := range esAllRows {
		if slices.Contains(typs, typ) {
			continue
		}
		for _, id := range ids {
			if !slices.Contains(rows, id) {
				out[typ] = append(out[typ], id)
			}
		}
	}
	return out
}

func assertESRows(t *testing.T, st *store.Store, want map[string][]string) {
	t.Helper()
	for _, typ := range []string{TypeEventSchemasDiscoverer, TypeEventSchemasRegistry, TypeEventSchemasSchema, TypeEventSchemasRegistryPolicy} {
		got := nativeIDs(rowsByNativeID(t, st, typ))
		w := slices.Clone(want[typ])
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s rows = %v, want %v", typ, got, w)
		}
	}
}

// assertESRow checks a stored row's name and status (nil for none),
// attributes and tags (nil wantTags: no tags stored).
func assertESRow(t *testing.T, row store.Resource, name, status *string, attrs any, wantTags map[string]string) {
	t.Helper()
	if sv(row.Name) != sv(name) || (name == nil) != (row.Name == nil) || sv(row.Region) != testRegion || row.AttributesJSON != mustJSON(attrs) {
		t.Errorf("%s: name/region = %v/%q, attrs = %s; want %v/%q, %s", row.NativeID, row.Name, sv(row.Region), row.AttributesJSON, name, testRegion, mustJSON(attrs))
	}
	if sv(row.Status) != sv(status) || (status == nil) != (row.Status == nil) {
		t.Errorf("%s: status = %v, want %v", row.NativeID, row.Status, status)
	}
	if wantTags == nil {
		if row.TagsJSON != nil {
			t.Errorf("%s: tags = %s, want none", row.NativeID, *row.TagsJSON)
		}
		return
	}
	assertTagsMap(t, row.TagsJSON, wantTags)
}

// assertESWarnings checks op warned exactly once per key in keys, each
// warning naming its key.
func assertESWarnings(t *testing.T, run esRun, op string, keys ...string) {
	t.Helper()
	got := run.warns(op)
	if len(got) != len(keys) {
		t.Errorf("%s warnings = %d (%v), want %d naming %v", op, len(got), got, len(keys), keys)
		return
	}
	for _, k := range keys {
		if !slices.ContainsFunc(got, func(w store.ScanWarning) bool { return strings.Contains(w.Message, k) }) {
			t.Errorf("%s warnings %v name no %q", op, got, k)
		}
	}
}

func TestScanEventSchemas_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	calls := newESCalls()
	run := scanESTopLevel(t, st, nil, calls)
	if run.err != nil {
		t.Fatalf("scanEventSchemas: %v", run.err)
	}
	if run.total != 10 || run.inserted != 10 {
		t.Errorf("total, inserted = %d, %d; want 10, 10", run.total, run.inserted)
	}
	assertESRows(t, st, esAllRows)

	f := newESFixture()
	ds := rowsByNativeID(t, st, TypeEventSchemasDiscoverer)
	assertESRow(t, ds[esARN("discoverer", "d-1")], ptrStr("d-1"), ptrStr("STARTED"), f.discoverers[0][0], esTags)
	assertESRow(t, ds[esARN("discoverer", "d-2")], ptrStr("d-2"), nil, f.discoverers[1][0], nil)
	regs := rowsByNativeID(t, st, TypeEventSchemasRegistry)
	assertESRow(t, regs[esARN("registry", "reg-a")], ptrStr("reg-a"), nil, f.registries[0][0], esTags)
	assertESRow(t, regs[esARN("registry", "reg-b")], ptrStr("reg-b"), nil, f.registries[1][0], nil)
	assertESRow(t, regs[esARN("registry", "noname")], nil, nil, f.registries[1][3], nil)
	ss := rowsByNativeID(t, st, TypeEventSchemasSchema)
	assertESRow(t, ss[esARN("schema", "reg-a/s1")], ptrStr("s1"), nil, f.schemas["reg-a"][0][0], esTags)
	assertESRow(t, ss[esARN("schema", "reg-a/s2")], ptrStr("s2"), nil, f.schemas["reg-a"][1][0], nil)
	ps := rowsByNativeID(t, st, TypeEventSchemasRegistryPolicy)
	assertESRow(t, ps[esPolicyARN("reg-a")], ptrStr("reg-a"), nil, f.policies["reg-a"], nil)

	assertESWarnings(t, run, "ListDiscoverers", "d-noarn")
	assertESWarnings(t, run, "ListRegistries", "reg-noarn", esARN("registry", "noname"))
	assertESWarnings(t, run, "ListSchemas", "s-noarn")
	assertESWarnings(t, run, "GetResourcePolicy")
	for _, op := range []string{"ListSchemas", "GetResourcePolicy"} {
		if got, want := calls[op].distinct(), []string{"reg-a", "reg-b", "reg-c"}; !slices.Equal(got, want) {
			t.Errorf("%s called for %v, want %v", op, got, want)
		}
	}
}

// A failed page two keeps the rows read before it, the error is returned, and
// every later phase still runs over the registries stored.
func TestScanEventSchemas_FailedPageKeepsRowsAndRunsLaterPhases(t *testing.T) {
	for _, tc := range []struct {
		op, parent string
		want       map[string][]string
	}{
		{"ListDiscoverers", "", esWithout(nil, esARN("discoverer", "d-2"))},
		{"ListRegistries", "", map[string][]string{
			TypeEventSchemasDiscoverer:     esAllRows[TypeEventSchemasDiscoverer],
			TypeEventSchemasRegistry:       {esARN("registry", "reg-a")},
			TypeEventSchemasSchema:         {esARN("schema", "reg-a/s1"), esARN("schema", "reg-a/s2")},
			TypeEventSchemasRegistryPolicy: {esPolicyARN("reg-a")},
		}},
		{"ListSchemas", "reg-a", esWithout(nil, esARN("schema", "reg-a/s2"))},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			run := scanESTopLevel(t, st, map[string]map[string]error{tc.op: {tc.parent + "/p2": esBadReq}}, newESCalls())
			if classifyServiceError(run.err) != outcomeError || !isAPIErrorCode(run.err, "BadRequestException") {
				t.Fatalf("err = %v, want a BadRequestException error", run.err)
			}
			assertESRows(t, st, tc.want)
			n := 0
			for _, ids := range tc.want {
				n += len(ids)
			}
			if run.total != n || run.inserted != n {
				t.Errorf("total, inserted = %d, %d; want %d, %d", run.total, run.inserted, n, n)
			}
		})
	}
}

// A denied op warns once, naming the first key it lost when it fans out over
// registries, and is no error; the other phases keep their rows.
func TestScanEventSchemas_DeniedOpWarnsOnce(t *testing.T) {
	for _, tc := range []struct {
		op   string
		fail map[string]error
		key  string
		want map[string][]string
	}{
		{"ListDiscoverers", map[string]error{"/": esForbidden}, "", esWithout([]string{TypeEventSchemasDiscoverer})},
		{"ListRegistries", map[string]error{"/": esForbidden}, "", esWithout([]string{TypeEventSchemasRegistry, TypeEventSchemasSchema, TypeEventSchemasRegistryPolicy})},
		{"ListSchemas", map[string]error{"reg-a/": esForbidden, "reg-b/": esForbidden}, "reg-a", esWithout([]string{TypeEventSchemasSchema})},
		{"GetResourcePolicy", map[string]error{"reg-a": esForbidden, "reg-b": esForbidden}, "reg-a", esWithout([]string{TypeEventSchemasRegistryPolicy})},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			run := scanESTopLevel(t, st, map[string]map[string]error{tc.op: tc.fail}, newESCalls())
			if run.err != nil {
				t.Fatalf("err = %v, want nil", run.err)
			}
			assertESWarnings(t, run, tc.op, tc.key)
			assertESRows(t, st, tc.want)
		})
	}
}

// GetResourcePolicy errors other than its modeled NotFoundException and a
// denial, an unmodeled not-found and a throttle included, are returned after
// the other registries' policies are stored.
func TestScanEventSchemas_PolicyErrorsAreReturned(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want serviceOutcome
	}{
		{"bad request", esBadReq, outcomeError},
		{"unmodeled not-found", apiErr("ResourceNotFoundException", "gone"), outcomeError},
		{"throttled", esThrottled, outcomeTransient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			run := scanESTopLevel(t, st, map[string]map[string]error{"GetResourcePolicy": {"reg-b": tc.err}}, newESCalls())
			if got := classifyServiceError(run.err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", run.err, got, tc.want)
			}
			if !errors.Is(run.err, tc.err) {
				t.Errorf("err = %v, want it to wrap %v", run.err, tc.err)
			}
			assertESRows(t, st, esAllRows)
			if run.total != 10 || run.inserted != 10 {
				t.Errorf("total, inserted = %d, %d; want 10, 10", run.total, run.inserted)
			}
			assertESWarnings(t, run, "GetResourcePolicy")
		})
	}
}

// A throttled phase alone leaves the scan transient; beside another phase's
// hard error it is warned about once and the scan reads as an error.
func TestScanEventSchemas_TransientPhaseBesideHardError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hard      bool
		want      serviceOutcome
		wantWarns int
	}{
		{"alone", false, outcomeTransient, 0},
		{"beside a hard error", true, outcomeError, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			fail := map[string]map[string]error{"ListDiscoverers": {"/p2": esThrottled}}
			if tc.hard {
				fail["GetResourcePolicy"] = map[string]error{"reg-a": esBadReq}
			}
			run := scanESTopLevel(t, st, fail, newESCalls())
			if got := classifyServiceError(run.err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", run.err, got, tc.want)
			}
			if got := len(run.warns("ListDiscoverers")); got != tc.wantWarns {
				t.Errorf("ListDiscoverers warnings = %d, want %d", got, tc.wantWarns)
			}
		})
	}
}

// A failed registry write stops the scan with no warning claiming the lost
// rows, and no schema or policy is read; the discoverers stay stored.
func TestScanEventSchemas_RegistryWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeEventSchemasRegistry)
	calls := newESCalls()
	run := scanESTopLevel(t, st, nil, calls)
	if !errors.Is(run.err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", run.err)
	}
	assertESWarnings(t, run, "ListRegistries")
	for _, op := range []string{"ListSchemas", "GetResourcePolicy"} {
		if got := calls[op].distinct(); len(got) != 0 {
			t.Errorf("%s called for %v; want no call", op, got)
		}
	}
	assertESRows(t, st, map[string][]string{TypeEventSchemasDiscoverer: esAllRows[TypeEventSchemasDiscoverer]})
}

// ListSchemas models no not-found code, so a registry the service no longer
// finds fails the phase; the other registries' schemas are still stored.
func TestScanEventSchemas_SchemasNotFoundFailsPhase(t *testing.T) {
	st := newTestStore(t)
	run := scanESTopLevel(t, st, map[string]map[string]error{"ListSchemas": {"reg-b/": esNotFound}}, newESCalls())
	if classifyServiceError(run.err) != outcomeError || !errors.Is(run.err, esNotFound) {
		t.Fatalf("err = %v, want the NotFoundException", run.err)
	}
	assertESRows(t, st, esWithout(nil, esARN("schema", "reg-b/s3")))
	if run.total != 9 || run.inserted != 9 {
		t.Errorf("total, inserted = %d, %d; want 9, 9", run.total, run.inserted)
	}
}

// esRunWith runs scanEventSchemas under ctx with the fixture's handlers,
// each passed through wrap first.
func esRunWith(ctx context.Context, st *store.Store, wrap func(op string, h func(in any) (any, error)) func(in any) (any, error)) (int, int, error) {
	h := esHandlers(newESFixture(), nil, newESCalls())
	for op, fn := range h {
		h[op] = wrap(op, fn)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, esForbidden)
	return scanEventSchemas(ctx, acct, testRegion, st, testScanID)
}

// A context ended after the schemas were read stops the policy phase, and
// the scan returns that stop.
func TestScanEventSchemas_ContextEndStopsPolicyPhase(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu    sync.Mutex
		reads int
	)
	_, _, err := esRunWith(ctx, st, func(op string, h func(in any) (any, error)) func(in any) (any, error) {
		return func(in any) (any, error) {
			if op == "GetResourcePolicy" {
				t.Errorf("GetResourcePolicy called after the context ended")
			}
			out, err := h(in)
			if op == "ListSchemas" {
				mu.Lock()
				defer mu.Unlock()
				// reg-a has two pages, so the fourth call is the last.
				if reads++; reads == 4 {
					cancel()
				}
			}
			return out, err
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", err)
	}
	assertESRows(t, st, esWithout([]string{TypeEventSchemasRegistryPolicy}))
}

// A failed discoverer write stops the scan before the registries are listed.
func TestScanEventSchemas_DiscovererWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeEventSchemasDiscoverer)
	var calls parentCalls
	_, _, err := esRunWith(context.Background(), st, func(op string, h func(in any) (any, error)) func(in any) (any, error) {
		return func(in any) (any, error) {
			calls.add(op)
			return h(in)
		}
	})
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"ListDiscoverers"}) {
		t.Errorf("ops called = %v; want only ListDiscoverers", got)
	}
}
