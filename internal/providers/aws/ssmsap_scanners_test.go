package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssmsap"
	ssmsaptypes "github.com/aws/aws-sdk-go-v2/service/ssmsap/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	sapDenied       = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: ssm-sap:List"}
	sapUnauthorized = &smithy.GenericAPIError{Code: "UnauthorizedException", Message: "The request is not authorized."}
	sapInvalid      = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	sapNotFound     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}
	sapThrottled    = apiErr("ThrottlingException", "Rate exceeded")
)

var sapTags = map[string]string{"team": "sap"}

func sapAppARN(app string) string {
	return fmt.Sprintf("arn:aws:ssm-sap:%s:%s:SAP_ABAP/%s", testRegion, testAccountID, app)
}

func sapCompARN(app, comp string) string { return sapAppARN(app) + "/COMPONENT/" + comp }

func sapDBARN(app, db string) string { return sapAppARN(app) + "/DB/" + db }

// sapFixture is what the stub lists: the application list and app-a's
// listings have two pages, each page-one element is tagged and the others
// are not, and each listing holds an element with no ARN. The application
// with ARN sapAppARN("noid") has no Id. app-a and app-b each hold a
// component named HDB.
type sapFixture struct {
	apps  [][]ssmsaptypes.ApplicationSummary
	comps map[string][][]ssmsaptypes.ComponentSummary // by application Id
	dbs   map[string][][]ssmsaptypes.DatabaseSummary  // by application Id
}

func newSAPFixture() sapFixture {
	app := func(id string, status ssmsaptypes.ApplicationDiscoveryStatus, tags map[string]string) ssmsaptypes.ApplicationSummary {
		return ssmsaptypes.ApplicationSummary{Arn: ptrStr(sapAppARN(id)), Id: ptrStr(id), Type: ssmsaptypes.ApplicationTypeSapAbap, DiscoveryStatus: status, Tags: tags}
	}
	comp := func(app, id string, typ ssmsaptypes.ComponentType, tags map[string]string) ssmsaptypes.ComponentSummary {
		return ssmsaptypes.ComponentSummary{Arn: ptrStr(sapCompARN(app, id)), ApplicationId: ptrStr(app), ComponentId: ptrStr(id), ComponentType: typ, Tags: tags}
	}
	db := func(app, comp, id string, typ ssmsaptypes.DatabaseType, tags map[string]string) ssmsaptypes.DatabaseSummary {
		return ssmsaptypes.DatabaseSummary{Arn: ptrStr(sapDBARN(app, id)), ApplicationId: ptrStr(app), ComponentId: ptrStr(comp), DatabaseId: ptrStr(id), DatabaseType: typ, Tags: tags}
	}
	return sapFixture{
		apps: [][]ssmsaptypes.ApplicationSummary{
			{app("app-a", ssmsaptypes.ApplicationDiscoveryStatusSuccess, sapTags)},
			{app("app-b", "", nil), {Id: ptrStr("app-noarn")}, {Arn: ptrStr(sapAppARN("noid"))}},
		},
		comps: map[string][][]ssmsaptypes.ComponentSummary{
			"app-a": {{comp("app-a", "HDB", ssmsaptypes.ComponentTypeHana, sapTags)}, {comp("app-a", "ABAP", "", nil), {ComponentId: ptrStr("c-noarn")}}},
			"app-b": {{comp("app-b", "HDB", ssmsaptypes.ComponentTypeHana, nil)}},
		},
		dbs: map[string][][]ssmsaptypes.DatabaseSummary{
			"app-a": {{db("app-a", "HDB", "SYSTEMDB", ssmsaptypes.DatabaseTypeSystem, sapTags)}, {db("app-a", "HDB", "HDB", "", nil), {DatabaseId: ptrStr("db-noarn")}}},
			"app-b": {{db("app-b", "HDB", "SYSTEMDB", ssmsaptypes.DatabaseTypeSystem, nil)}},
		},
	}
}

// sapPinned fails a call that does not ask for the model's page cap or that
// sets a filter: either changes what the listing returns.
func sapPinned(maxResults *int32, filters ...any) error {
	if maxResults == nil || *maxResults != 50 {
		return fmt.Errorf("MaxResults = %v, want 50", maxResults)
	}
	for _, f := range filters {
		if f != nil {
			return fmt.Errorf("filter %v set, want none", f)
		}
	}
	return nil
}

// sapHandlers serves f; fail is keyed by op, then by "<app>/<token>" (app ""
// for ListApplications; token "" on page one). calls records each
// per-application call.
func sapHandlers(f sapFixture, fail map[string]map[string]error, calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApplications": func(in any) (any, error) {
			p := in.(*ssmsap.ListApplicationsInput)
			var filters any
			if p.Filters != nil {
				filters = p.Filters
			}
			if err := sapPinned(p.MaxResults, filters); err != nil {
				return nil, err
			}
			items, next, err := esServe(f.apps, "", p.NextToken, fail["ListApplications"])
			if err != nil {
				return nil, err
			}
			return &ssmsap.ListApplicationsOutput{Applications: items, NextToken: next}, nil
		},
		"ListComponents": func(in any) (any, error) {
			p := in.(*ssmsap.ListComponentsInput)
			if err := sapPinned(p.MaxResults); err != nil {
				return nil, err
			}
			app := sv(p.ApplicationId)
			calls["ListComponents"].add(app)
			items, next, err := esServe(f.comps[app], app, p.NextToken, fail["ListComponents"])
			if err != nil {
				return nil, err
			}
			return &ssmsap.ListComponentsOutput{Components: items, NextToken: next}, nil
		},
		"ListDatabases": func(in any) (any, error) {
			p := in.(*ssmsap.ListDatabasesInput)
			var filters any
			if p.ComponentId != nil {
				filters = *p.ComponentId
			}
			if err := sapPinned(p.MaxResults, filters); err != nil {
				return nil, err
			}
			app := sv(p.ApplicationId)
			calls["ListDatabases"].add(app)
			items, next, err := esServe(f.dbs[app], app, p.NextToken, fail["ListDatabases"])
			if err != nil {
				return nil, err
			}
			return &ssmsap.ListDatabasesOutput{Databases: items, NextToken: next}, nil
		},
	}
}

func newSAPCalls() map[string]*parentCalls {
	return map[string]*parentCalls{"ListComponents": {}, "ListDatabases": {}}
}

var sapOpLabels = map[string]string{
	"ListApplications": "ssm-sap:ListApplications",
	"ListComponents":   "ssm-sap:ListComponents",
	"ListDatabases":    "ssm-sap:ListDatabases",
}

// sapRun is what one scanSSMSAP run returned and warned.
type sapRun struct {
	total, inserted int
	err             error
	warnings        []store.ScanWarning
}

// sapScan runs scanSSMSAP under ctx with the fixture's handlers, each passed
// through wrap (nil for none).
func sapScan(ctx context.Context, st *store.Store, fail map[string]map[string]error, calls map[string]*parentCalls, wrap func(op string, h func(in any) (any, error)) func(in any) (any, error)) sapRun {
	var (
		mu  sync.Mutex
		run sapRun
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		run.warnings = append(run.warnings, w)
	}
	h := sapHandlers(newSAPFixture(), fail, calls)
	if wrap != nil {
		for op, fn := range h {
			h[op] = wrap(op, fn)
		}
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, sapDenied)
	run.total, run.inserted, run.err = scanSSMSAP(ctx, acct, testRegion, st, testScanID)
	return run
}

func scanSAPTopLevel(st *store.Store, fail map[string]map[string]error, calls map[string]*parentCalls) sapRun {
	return sapScan(context.Background(), st, fail, calls, nil)
}

// sapAllRows is every NativeID the full fixture stores, per type.
var sapAllRows = map[string][]string{
	TypeSSMSAPApplication:          {sapAppARN("app-a"), sapAppARN("app-b"), sapAppARN("noid")},
	TypeSystemsManagerSAPComponent: {sapCompARN("app-a", "HDB"), sapCompARN("app-a", "ABAP"), sapCompARN("app-b", "HDB")},
	TypeSystemsManagerSAPDatabase:  {sapDBARN("app-a", "SYSTEMDB"), sapDBARN("app-a", "HDB"), sapDBARN("app-b", "SYSTEMDB")},
}

// sapWithout is sapAllRows with types typs, and the given rows, removed.
func sapWithout(typs []string, rows ...string) map[string][]string {
	out := map[string][]string{}
	for typ, ids := range sapAllRows {
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

func sapCount(rows map[string][]string) int {
	n := 0
	for _, ids := range rows {
		n += len(ids)
	}
	return n
}

// assertSAPRows checks the stored rows per type and the run's counts.
func assertSAPRows(t *testing.T, st *store.Store, run sapRun, want map[string][]string) {
	t.Helper()
	for _, typ := range []string{TypeSSMSAPApplication, TypeSystemsManagerSAPComponent, TypeSystemsManagerSAPDatabase} {
		got := nativeIDs(rowsByNativeID(t, st, typ))
		w := slices.Clone(want[typ])
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s rows = %v, want %v", typ, got, w)
		}
	}
	if n := sapCount(want); run.total != n || run.inserted != n {
		t.Errorf("total, inserted = %d, %d; want %d, %d", run.total, run.inserted, n, n)
	}
}

// assertSAPWarnings checks op warned exactly once per key in keys, each
// warning naming its key.
func assertSAPWarnings(t *testing.T, run sapRun, op string, keys ...string) {
	t.Helper()
	var got []store.ScanWarning
	for _, w := range run.warnings {
		if w.Service == sapOpLabels[op] {
			got = append(got, w)
		}
	}
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

func TestScanSSMSAP_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	calls := newSAPCalls()
	run := scanSAPTopLevel(st, nil, calls)
	if run.err != nil {
		t.Fatalf("scanSSMSAP: %v", run.err)
	}
	assertSAPRows(t, st, run, sapAllRows)

	f := newSAPFixture()
	apps := rowsByNativeID(t, st, TypeSSMSAPApplication)
	assertESRow(t, apps[sapAppARN("app-a")], ptrStr("app-a"), ptrStr("SUCCESS"), f.apps[0][0], sapTags)
	assertESRow(t, apps[sapAppARN("app-b")], ptrStr("app-b"), nil, f.apps[1][0], nil)
	assertESRow(t, apps[sapAppARN("noid")], nil, nil, f.apps[1][2], nil)
	comps := rowsByNativeID(t, st, TypeSystemsManagerSAPComponent)
	assertESRow(t, comps[sapCompARN("app-a", "HDB")], ptrStr("HDB"), nil, f.comps["app-a"][0][0], sapTags)
	assertESRow(t, comps[sapCompARN("app-a", "ABAP")], ptrStr("ABAP"), nil, f.comps["app-a"][1][0], nil)
	dbs := rowsByNativeID(t, st, TypeSystemsManagerSAPDatabase)
	assertESRow(t, dbs[sapDBARN("app-a", "SYSTEMDB")], ptrStr("SYSTEMDB"), nil, f.dbs["app-a"][0][0], sapTags)
	assertESRow(t, dbs[sapDBARN("app-a", "HDB")], ptrStr("HDB"), nil, f.dbs["app-a"][1][0], nil)

	assertSAPWarnings(t, run, "ListApplications", "app-noarn", sapAppARN("noid"))
	assertSAPWarnings(t, run, "ListComponents", "c-noarn")
	assertSAPWarnings(t, run, "ListDatabases", "db-noarn")
	for _, op := range []string{"ListComponents", "ListDatabases"} {
		if got, want := calls[op].distinct(), []string{"app-a", "app-b"}; !slices.Equal(got, want) {
			t.Errorf("%s called for %v, want %v", op, got, want)
		}
	}
}

// A failed page two keeps the rows read before it, the error is returned, and
// every later phase still runs over the applications stored.
func TestScanSSMSAP_FailedPageKeepsRowsAndRunsLaterPhases(t *testing.T) {
	for _, tc := range []struct {
		op, parent string
		want       map[string][]string
	}{
		{"ListApplications", "", map[string][]string{
			TypeSSMSAPApplication:          {sapAppARN("app-a")},
			TypeSystemsManagerSAPComponent: {sapCompARN("app-a", "HDB"), sapCompARN("app-a", "ABAP")},
			TypeSystemsManagerSAPDatabase:  {sapDBARN("app-a", "SYSTEMDB"), sapDBARN("app-a", "HDB")},
		}},
		{"ListComponents", "app-a", sapWithout(nil, sapCompARN("app-a", "ABAP"))},
		{"ListDatabases", "app-a", sapWithout(nil, sapDBARN("app-a", "HDB"))},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			run := scanSAPTopLevel(st, map[string]map[string]error{tc.op: {tc.parent + "/p2": sapInvalid}}, newSAPCalls())
			if classifyServiceError(run.err) != outcomeError || !errors.Is(run.err, sapInvalid) {
				t.Fatalf("err = %v, want the ValidationException", run.err)
			}
			assertSAPRows(t, st, run, tc.want)
		})
	}
}

// A denial warns once per op, naming the first application it lost when the
// op fans out over applications, and is no error; the other phases keep
// their rows. ListComponents models UnauthorizedException as its denial.
func TestScanSSMSAP_DeniedOpWarnsOnce(t *testing.T) {
	both := func(err error) map[string]error { return map[string]error{"app-a/": err, "app-b/": err} }
	for _, tc := range []struct {
		name, op string
		fail     map[string]error
		key      string
		want     map[string][]string
	}{
		{"applications", "ListApplications", map[string]error{"/": sapDenied}, "", map[string][]string{}},
		{"components unauthorized", "ListComponents", both(sapUnauthorized), "app-a", sapWithout([]string{TypeSystemsManagerSAPComponent})},
		{"components access denied", "ListComponents", both(sapDenied), "app-a", sapWithout([]string{TypeSystemsManagerSAPComponent})},
		{"components unauthorized then denied", "ListComponents", map[string]error{"app-a/": sapUnauthorized, "app-b/": sapDenied}, "app-a", sapWithout([]string{TypeSystemsManagerSAPComponent})},
		{"components denied then unauthorized", "ListComponents", map[string]error{"app-a/": sapDenied, "app-b/": sapUnauthorized}, "app-a", sapWithout([]string{TypeSystemsManagerSAPComponent})},
		{"components unauthorized on page two", "ListComponents", map[string]error{"app-a/p2": sapUnauthorized}, "app-a", sapWithout(nil, sapCompARN("app-a", "ABAP"))},
		{"databases", "ListDatabases", both(sapDenied), "app-a", sapWithout([]string{TypeSystemsManagerSAPDatabase})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			run := scanSAPTopLevel(st, map[string]map[string]error{tc.op: tc.fail}, newSAPCalls())
			if run.err != nil {
				t.Fatalf("err = %v, want nil", run.err)
			}
			// Every case loses the page holding the op's no-ARN element, so
			// the denial is the op's only warning.
			assertSAPWarnings(t, run, tc.op, tc.key)
			assertSAPRows(t, st, run, tc.want)
		})
	}
}

// ResourceNotFoundException, the code both child ops model for a deleted
// application, skips the application: silently when nothing was read from
// it, with a warning naming it when a page had been read.
func TestScanSSMSAP_DeletedApplicationIsSkipped(t *testing.T) {
	for _, tc := range []struct {
		op, key string
		lost    []string
		warns   []string
	}{
		{"ListComponents", "app-b/", []string{sapCompARN("app-b", "HDB")}, []string{"c-noarn"}},
		{"ListComponents", "app-a/p2", []string{sapCompARN("app-a", "ABAP")}, []string{"app-a"}},
		{"ListDatabases", "app-b/", []string{sapDBARN("app-b", "SYSTEMDB")}, []string{"db-noarn"}},
		{"ListDatabases", "app-a/p2", []string{sapDBARN("app-a", "HDB")}, []string{"app-a"}},
	} {
		t.Run(tc.op+" "+tc.key, func(t *testing.T) {
			st := newTestStore(t)
			run := scanSAPTopLevel(st, map[string]map[string]error{tc.op: {tc.key: sapNotFound}}, newSAPCalls())
			if run.err != nil {
				t.Fatalf("err = %v, want nil", run.err)
			}
			assertSAPRows(t, st, run, sapWithout(nil, tc.lost...))
			assertSAPWarnings(t, run, tc.op, tc.warns...)
		})
	}
}

// Any other per-application error, a throttle and a not-found on the
// unfiltered application list included, is returned after the other rows are
// stored: the lost children have no listed row to keep in their place.
func TestScanSSMSAP_OtherErrorsAreReturned(t *testing.T) {
	for _, tc := range []struct {
		name, op, key string
		err           error
		want          serviceOutcome
		rows          map[string][]string
	}{
		{"components invalid", "ListComponents", "app-b/", sapInvalid, outcomeError, sapWithout(nil, sapCompARN("app-b", "HDB"))},
		{"components throttled", "ListComponents", "app-b/", sapThrottled, outcomeTransient, sapWithout(nil, sapCompARN("app-b", "HDB"))},
		{"databases throttled", "ListDatabases", "app-b/", sapThrottled, outcomeTransient, sapWithout(nil, sapDBARN("app-b", "SYSTEMDB"))},
		{"applications not found", "ListApplications", "/", sapNotFound, outcomeError, map[string][]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			run := scanSAPTopLevel(st, map[string]map[string]error{tc.op: {tc.key: tc.err}}, newSAPCalls())
			if got := classifyServiceError(run.err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", run.err, got, tc.want)
			}
			if !errors.Is(run.err, tc.err) {
				t.Errorf("err = %v, want it to wrap %v", run.err, tc.err)
			}
			assertSAPRows(t, st, run, tc.rows)
		})
	}
}

// A throttled phase alone leaves the scan transient; beside another phase's
// hard error it is warned about once and the scan reads as an error.
func TestScanSSMSAP_TransientPhaseBesideHardError(t *testing.T) {
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
			fail := map[string]map[string]error{"ListComponents": {"app-b/": sapThrottled}}
			if tc.hard {
				fail["ListDatabases"] = map[string]error{"app-a/": sapInvalid}
			}
			run := scanSAPTopLevel(st, fail, newSAPCalls())
			if got := classifyServiceError(run.err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", run.err, got, tc.want)
			}
			n := 0
			for _, w := range run.warnings {
				if w.Service == "ssm-sap:ListComponents" && strings.Contains(w.Message, "app-b") {
					n++
				}
			}
			if n != tc.wantWarns {
				t.Errorf("ListComponents warnings naming app-b = %d, want %d", n, tc.wantWarns)
			}
		})
	}
}

// A failed write stops the scan with no warning claiming the lost rows, and
// no later phase runs; the rows of earlier phases stay stored.
func TestScanSSMSAP_WriteFailureStops(t *testing.T) {
	for _, tc := range []struct {
		typ, op  string
		ops      []string
		keptRows map[string][]string
	}{
		{TypeSSMSAPApplication, "ListApplications", []string{"ListApplications"}, map[string][]string{}},
		{TypeSystemsManagerSAPComponent, "ListComponents", []string{"ListApplications", "ListComponents"}, sapWithout([]string{TypeSystemsManagerSAPComponent, TypeSystemsManagerSAPDatabase})},
		{TypeSystemsManagerSAPDatabase, "ListDatabases", []string{"ListApplications", "ListComponents", "ListDatabases"}, sapWithout([]string{TypeSystemsManagerSAPDatabase}, sapCompARN("app-b", "HDB"))},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			st := newTestStore(t)
			refuseStoreWrites(t, st, tc.typ)
			var ops parentCalls
			fail := map[string]map[string]error{"ListComponents": {"app-b/": sapUnauthorized}}
			run := sapScan(context.Background(), st, fail, newSAPCalls(), func(op string, h func(in any) (any, error)) func(in any) (any, error) {
				return func(in any) (any, error) {
					ops.add(op)
					return h(in)
				}
			})
			if !errors.Is(run.err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want the store write failure", run.err)
			}
			assertSAPWarnings(t, run, tc.op)
			if got := ops.distinct(); !slices.Equal(got, tc.ops) {
				t.Errorf("ops called = %v; want %v", got, tc.ops)
			}
			for _, typ := range []string{TypeSSMSAPApplication, TypeSystemsManagerSAPComponent, TypeSystemsManagerSAPDatabase} {
				want := slices.Clone(tc.keptRows[typ])
				slices.Sort(want)
				if got := nativeIDs(rowsByNativeID(t, st, typ)); !slices.Equal(got, want) {
					t.Errorf("%s rows = %v, want %v", typ, got, want)
				}
			}
		})
	}
}

// A context ended after the components were read stops the database phase,
// and the scan returns that stop.
func TestScanSSMSAP_ContextEndStopsDatabasePhase(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu    sync.Mutex
		reads int
	)
	run := sapScan(ctx, st, nil, newSAPCalls(), func(op string, h func(in any) (any, error)) func(in any) (any, error) {
		return func(in any) (any, error) {
			if op == "ListDatabases" {
				t.Errorf("ListDatabases called after the context ended")
			}
			out, err := h(in)
			if op == "ListComponents" {
				mu.Lock()
				defer mu.Unlock()
				// app-a has two pages and app-b one, so the third call is the last.
				if reads++; reads == 3 {
					cancel()
				}
			}
			return out, err
		}
	})
	if !errors.Is(run.err, context.Canceled) {
		t.Fatalf("err = %v, want it to wrap context.Canceled", run.err)
	}
	assertSAPRows(t, st, run, sapWithout([]string{TypeSystemsManagerSAPDatabase}))
}
