package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/migrationhubrefactorspaces"
	rstypes "github.com/aws/aws-sdk-go-v2/service/migrationhubrefactorspaces/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	rfsDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: refactor-spaces:List"}
	rfsBadInput  = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	rfsGone      = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	rfsThrottled = apiErr("ThrottlingException", "Rate exceeded")
)

func rfsEnvARN(env string) string {
	return fmt.Sprintf("arn:aws:refactor-spaces:%s:%s:environment/%s", testRegion, testAccountID, env)
}

func rfsAppARN(env, app string) string { return rfsEnvARN(env) + "/application/" + app }

// rfsFixture is what the stub lists: every page-one element is tagged, the
// page-two and second-parent elements are not, and each listing holds an
// element with no Arn. Environment env-3 and application app-3 have no id.
type rfsFixture struct {
	envs   [][]rstypes.EnvironmentSummary
	apps   map[string][][]rstypes.ApplicationSummary // by environment id
	svcs   map[string][][]rstypes.ServiceSummary     // by application id
	routes map[string][][]rstypes.RouteSummary       // by application id
}

var rfsTags = map[string]string{"team": "rs"}

// rfsAppEnv is the environment each application with an id belongs to.
var rfsAppEnv = map[string]string{"app-1": "env-1", "app-2": "env-2"}

func newRSFixture() rfsFixture {
	app := func(env, id string, tags map[string]string) rstypes.ApplicationSummary {
		return rstypes.ApplicationSummary{Arn: ptrStr(rfsAppARN(env, id)), ApplicationId: ptrStr(id), EnvironmentId: ptrStr(env), Name: ptrStr("name-" + id), Tags: tags}
	}
	svc := func(app, id string, tags map[string]string) rstypes.ServiceSummary {
		return rstypes.ServiceSummary{Arn: ptrStr(rfsAppARN(rfsAppEnv[app], app) + "/service/" + id), ServiceId: ptrStr(id), ApplicationId: ptrStr(app), Name: ptrStr("name-" + id), Tags: tags}
	}
	route := func(app, id string, tags map[string]string) rstypes.RouteSummary {
		return rstypes.RouteSummary{Arn: ptrStr(rfsAppARN(rfsAppEnv[app], app) + "/route/" + id), RouteId: ptrStr(id), ApplicationId: ptrStr(app), SourcePath: ptrStr("/" + id), Tags: tags}
	}
	return rfsFixture{
		envs: [][]rstypes.EnvironmentSummary{
			{{Arn: ptrStr(rfsEnvARN("env-1")), EnvironmentId: ptrStr("env-1"), Name: ptrStr("name-env-1"), Tags: rfsTags}},
			{
				{Arn: ptrStr(rfsEnvARN("env-2")), EnvironmentId: ptrStr("env-2"), Name: ptrStr("name-env-2")},
				{EnvironmentId: ptrStr("env-noarn")},
				{Arn: ptrStr(rfsEnvARN("env-3")), Name: ptrStr("name-env-3")},
			},
		},
		apps: map[string][][]rstypes.ApplicationSummary{
			"env-1": {{app("env-1", "app-1", rfsTags)}, {{ApplicationId: ptrStr("app-noarn")}}},
			"env-2": {{app("env-2", "app-2", nil), {Arn: ptrStr(rfsAppARN("env-2", "app-3")), Name: ptrStr("name-app-3")}}},
		},
		svcs: map[string][][]rstypes.ServiceSummary{
			"app-1": {{svc("app-1", "svc-1", rfsTags)}, {{ServiceId: ptrStr("svc-noarn")}}},
			"app-2": {{svc("app-2", "svc-2", nil)}},
		},
		routes: map[string][][]rstypes.RouteSummary{
			"app-1": {{route("app-1", "rte-1", rfsTags)}, {{RouteId: ptrStr("rte-noarn")}}},
			"app-2": {{route("app-2", "rte-2", nil)}},
		},
	}
}

// rfsServe returns page token ("" or "p2") of pages, failing a call whose
// MaxResults is not the model cap or whose token names no page, and returning
// fail[parent+"/"+token] when set.
func rfsServe[Item any](pages [][]Item, parent string, token *string, maxResults *int32, fail map[string]error) ([]Item, *string, error) {
	if maxResults == nil || *maxResults != 100 {
		return nil, nil, fmt.Errorf("MaxResults = %v, want 100", maxResults)
	}
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

// rfsCheckApp checks a child call names a listed application under its own
// environment.
func rfsCheckApp(env, app *string) error {
	if want, ok := rfsAppEnv[sv(app)]; !ok || want != sv(env) {
		return fmt.Errorf("unexpected parent env %q app %q", sv(env), sv(app))
	}
	return nil
}

// rfsHandlers serves f; fail is keyed by op, then by "<parent>/<token>" (the
// parent is "" for ListEnvironments, the environment id or application id
// otherwise; the token is "" on page one). calls records each child call's
// parent id per op.
func rfsHandlers(f rfsFixture, fail map[string]map[string]error, calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListEnvironments": func(in any) (any, error) {
			p := in.(*migrationhubrefactorspaces.ListEnvironmentsInput)
			items, next, err := rfsServe(f.envs, "", p.NextToken, p.MaxResults, fail["ListEnvironments"])
			if err != nil {
				return nil, err
			}
			return &migrationhubrefactorspaces.ListEnvironmentsOutput{EnvironmentSummaryList: items, NextToken: next}, nil
		},
		"ListApplications": func(in any) (any, error) {
			p := in.(*migrationhubrefactorspaces.ListApplicationsInput)
			env := sv(p.EnvironmentIdentifier)
			calls["ListApplications"].add(env)
			items, next, err := rfsServe(f.apps[env], env, p.NextToken, p.MaxResults, fail["ListApplications"])
			if err != nil {
				return nil, err
			}
			return &migrationhubrefactorspaces.ListApplicationsOutput{ApplicationSummaryList: items, NextToken: next}, nil
		},
		"ListServices": func(in any) (any, error) {
			p := in.(*migrationhubrefactorspaces.ListServicesInput)
			if err := rfsCheckApp(p.EnvironmentIdentifier, p.ApplicationIdentifier); err != nil {
				return nil, err
			}
			app := sv(p.ApplicationIdentifier)
			calls["ListServices"].add(app)
			items, next, err := rfsServe(f.svcs[app], app, p.NextToken, p.MaxResults, fail["ListServices"])
			if err != nil {
				return nil, err
			}
			return &migrationhubrefactorspaces.ListServicesOutput{ServiceSummaryList: items, NextToken: next}, nil
		},
		"ListRoutes": func(in any) (any, error) {
			p := in.(*migrationhubrefactorspaces.ListRoutesInput)
			if err := rfsCheckApp(p.EnvironmentIdentifier, p.ApplicationIdentifier); err != nil {
				return nil, err
			}
			app := sv(p.ApplicationIdentifier)
			calls["ListRoutes"].add(app)
			items, next, err := rfsServe(f.routes[app], app, p.NextToken, p.MaxResults, fail["ListRoutes"])
			if err != nil {
				return nil, err
			}
			return &migrationhubrefactorspaces.ListRoutesOutput{RouteSummaryList: items, NextToken: next}, nil
		},
	}
}

func newRSCalls() map[string]*parentCalls {
	return map[string]*parentCalls{"ListApplications": {}, "ListServices": {}, "ListRoutes": {}}
}

var rfsOpLabels = map[string]string{
	"ListEnvironments": "migrationhubrefactorspaces:ListEnvironments",
	"ListApplications": "migrationhubrefactorspaces:ListApplications",
	"ListServices":     "migrationhubrefactorspaces:ListServices",
	"ListRoutes":       "migrationhubrefactorspaces:ListRoutes",
}

// scanRSTopLevel runs scanRefactorSpaces against the stub and returns its
// total, the warnings counted per op and its error.
func scanRSTopLevel(t *testing.T, st *store.Store, fail map[string]map[string]error, calls map[string]*parentCalls) (int, map[string]*int, error) {
	t.Helper()
	warns := map[string]*int{}
	for op, label := range rfsOpLabels {
		warns[op] = countOpWarnings(st, label)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(rfsHandlers(newRSFixture(), fail, calls), rfsDenied)
	total, _, err := scanRefactorSpaces(context.Background(), acct, testRegion, st, testScanID)
	return total, warns, err
}

// rfsAllRows is every NativeID the full fixture stores, per type.
var rfsAllRows = map[string][]string{
	TypeRefactorSpacesEnvironment: {rfsEnvARN("env-1"), rfsEnvARN("env-2"), rfsEnvARN("env-3")},
	TypeRefactorSpacesApplication: {rfsAppARN("env-1", "app-1"), rfsAppARN("env-2", "app-2"), rfsAppARN("env-2", "app-3")},
	TypeRefactorSpacesService:     {rfsAppARN("env-1", "app-1") + "/service/svc-1", rfsAppARN("env-2", "app-2") + "/service/svc-2"},
	TypeRefactorSpacesRoute:       {rfsAppARN("env-1", "app-1") + "/route/rte-1", rfsAppARN("env-2", "app-2") + "/route/rte-2"},
}

func assertRSRows(t *testing.T, st *store.Store, want map[string][]string) {
	t.Helper()
	for _, typ := range []string{TypeRefactorSpacesEnvironment, TypeRefactorSpacesApplication, TypeRefactorSpacesService, TypeRefactorSpacesRoute} {
		got := nativeIDs(rowsByNativeID(t, st, typ))
		w := slices.Clone(want[typ])
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s rows = %v, want %v", typ, got, w)
		}
	}
}

// assertRSRow checks a stored row's name (nil for none), attributes and tags
// (nil wantTags: no tags stored).
func assertRSRow(t *testing.T, row store.Resource, name *string, attrs any, wantTags map[string]string) {
	t.Helper()
	if sv(row.Name) != sv(name) || (name == nil) != (row.Name == nil) || sv(row.Region) != testRegion || row.AttributesJSON != mustJSON(attrs) {
		t.Errorf("%s: name/region = %v/%q, attrs = %s; want %v/%q, %s", row.NativeID, row.Name, sv(row.Region), row.AttributesJSON, name, testRegion, mustJSON(attrs))
	}
	if wantTags == nil {
		if row.TagsJSON != nil {
			t.Errorf("%s: tags = %s, want none", row.NativeID, *row.TagsJSON)
		}
		return
	}
	assertTagsMap(t, row.TagsJSON, wantTags)
}

func TestScanRefactorSpaces_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	calls := newRSCalls()
	total, warns, err := scanRSTopLevel(t, st, nil, calls)
	if err != nil {
		t.Fatalf("scanRefactorSpaces: %v", err)
	}
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
	assertRSRows(t, st, rfsAllRows)

	f := newRSFixture()
	envs := rowsByNativeID(t, st, TypeRefactorSpacesEnvironment)
	assertRSRow(t, envs[rfsEnvARN("env-1")], ptrStr("name-env-1"), f.envs[0][0], rfsTags)
	assertRSRow(t, envs[rfsEnvARN("env-2")], ptrStr("name-env-2"), f.envs[1][0], nil)
	apps := rowsByNativeID(t, st, TypeRefactorSpacesApplication)
	assertRSRow(t, apps[rfsAppARN("env-1", "app-1")], ptrStr("name-app-1"), f.apps["env-1"][0][0], rfsTags)
	assertRSRow(t, apps[rfsAppARN("env-2", "app-2")], ptrStr("name-app-2"), f.apps["env-2"][0][0], nil)
	svcs := rowsByNativeID(t, st, TypeRefactorSpacesService)
	assertRSRow(t, svcs[rfsAllRows[TypeRefactorSpacesService][0]], ptrStr("name-svc-1"), f.svcs["app-1"][0][0], rfsTags)
	assertRSRow(t, svcs[rfsAllRows[TypeRefactorSpacesService][1]], ptrStr("name-svc-2"), f.svcs["app-2"][0][0], nil)
	routes := rowsByNativeID(t, st, TypeRefactorSpacesRoute)
	assertRSRow(t, routes[rfsAllRows[TypeRefactorSpacesRoute][0]], nil, f.routes["app-1"][0][0], rfsTags)
	assertRSRow(t, routes[rfsAllRows[TypeRefactorSpacesRoute][1]], nil, f.routes["app-2"][0][0], nil)

	// One warning each for the element with no Arn, and for env-3 and app-3,
	// stored with no id, whose children were not read.
	for op, want := range map[string]int{"ListEnvironments": 2, "ListApplications": 2, "ListServices": 1, "ListRoutes": 1} {
		if *warns[op] != want {
			t.Errorf("%s warnings = %d, want %d", op, *warns[op], want)
		}
	}
	for op, want := range map[string][]string{
		"ListApplications": {"env-1", "env-2"},
		"ListServices":     {"app-1", "app-2"},
		"ListRoutes":       {"app-1", "app-2"},
	} {
		if got := calls[op].distinct(); !slices.Equal(got, want) {
			t.Errorf("%s called for %v, want %v", op, got, want)
		}
	}
}

// A failed page two keeps the rows read before it, the error is returned, and
// every later phase still runs over the parents stored.
func TestScanRefactorSpaces_FailedPageKeepsRowsAndRunsLaterPhases(t *testing.T) {
	for _, tc := range []struct {
		op, parent string
		want       map[string][]string
	}{
		{"ListEnvironments", "", map[string][]string{
			TypeRefactorSpacesEnvironment: {rfsEnvARN("env-1")},
			TypeRefactorSpacesApplication: {rfsAppARN("env-1", "app-1")},
			TypeRefactorSpacesService:     {rfsAllRows[TypeRefactorSpacesService][0]},
			TypeRefactorSpacesRoute:       {rfsAllRows[TypeRefactorSpacesRoute][0]},
		}},
		{"ListApplications", "env-1", rfsAllRows},
		{"ListServices", "app-1", rfsAllRows},
		{"ListRoutes", "app-1", rfsAllRows},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			_, _, err := scanRSTopLevel(t, st, map[string]map[string]error{tc.op: {tc.parent + "/p2": rfsBadInput}}, newRSCalls())
			if classifyServiceError(err) != outcomeError || !isAPIErrorCode(err, "ValidationException") {
				t.Fatalf("err = %v, want a ValidationException error", err)
			}
			assertRSRows(t, st, tc.want)
		})
	}
}

// A denied parent warns once for the op however many parents were denied, a
// parent gone since it was listed is skipped silently, and neither is an
// error; the other phases keep their rows.
func TestScanRefactorSpaces_DeniedOrGoneParents(t *testing.T) {
	without := func(typs ...string) map[string][]string {
		out := map[string][]string{}
		for typ, ids := range rfsAllRows {
			if !slices.Contains(typs, typ) {
				out[typ] = ids
			}
		}
		return out
	}
	for _, tc := range []struct {
		op      string
		parents []string
		want    map[string][]string
	}{
		{"ListEnvironments", []string{""}, without(TypeRefactorSpacesEnvironment, TypeRefactorSpacesApplication, TypeRefactorSpacesService, TypeRefactorSpacesRoute)},
		{"ListApplications", []string{"env-1", "env-2"}, without(TypeRefactorSpacesApplication, TypeRefactorSpacesService, TypeRefactorSpacesRoute)},
		{"ListServices", []string{"app-1", "app-2"}, without(TypeRefactorSpacesService)},
		{"ListRoutes", []string{"app-1", "app-2"}, without(TypeRefactorSpacesRoute)},
	} {
		for _, ec := range []struct {
			name     string
			err      error
			wantWarn int
		}{
			{"denied", rfsDenied, 1},
			{"gone", rfsGone, 0},
		} {
			if tc.op == "ListEnvironments" && ec.name == "gone" {
				continue // a parentless listing has no parent to vanish
			}
			t.Run(tc.op+"/"+ec.name, func(t *testing.T) {
				st := newTestStore(t)
				fail := map[string]error{}
				for _, p := range tc.parents {
					fail[p+"/"] = ec.err
				}
				_, warns, err := scanRSTopLevel(t, st, map[string]map[string]error{tc.op: fail}, newRSCalls())
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if *warns[tc.op] != ec.wantWarn {
					t.Errorf("%s warnings = %d, want %d", tc.op, *warns[tc.op], ec.wantWarn)
				}
				assertRSRows(t, st, tc.want)
			})
		}
	}
}

// ListEnvironments models ResourceNotFoundException, but with no parent to
// vanish it is returned, not skipped.
func TestScanRefactorSpaces_EnvironmentsNotFoundIsAnError(t *testing.T) {
	st := newTestStore(t)
	_, _, err := scanRSTopLevel(t, st, map[string]map[string]error{"ListEnvironments": {"/": rfsGone}}, newRSCalls())
	if !isAPIErrorCode(err, "ResourceNotFoundException") {
		t.Fatalf("err = %v, want ResourceNotFoundException", err)
	}
}

// A throttled phase alone leaves the scan transient; beside another phase's
// hard error it is warned about once and the scan reads as an error.
func TestScanRefactorSpaces_TransientPhaseBesideHardError(t *testing.T) {
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
			fail := map[string]map[string]error{"ListEnvironments": {"/p2": rfsThrottled}}
			if tc.hard {
				fail["ListRoutes"] = map[string]error{"app-1/": rfsBadInput}
			}
			_, warns, err := scanRSTopLevel(t, st, fail, newRSCalls())
			if got := classifyServiceError(err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", err, got, tc.want)
			}
			if *warns["ListEnvironments"] != tc.wantWarns {
				t.Errorf("ListEnvironments warnings = %d, want %d", *warns["ListEnvironments"], tc.wantWarns)
			}
		})
	}
}

// A failed application write stops the scan with no warning claiming the
// lost rows, and no service or route listing runs for the applications.
func TestScanRefactorSpaces_ApplicationWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeRefactorSpacesApplication)
	calls := newRSCalls()
	_, warns, err := scanRSTopLevel(t, st, nil, calls)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", err)
	}
	if *warns["ListApplications"] != 0 {
		t.Errorf("ListApplications warnings = %d; want none after a failed write", *warns["ListApplications"])
	}
	for _, op := range []string{"ListServices", "ListRoutes"} {
		if got := calls[op].distinct(); len(got) != 0 {
			t.Errorf("%s called for %v; want no call", op, got)
		}
	}
}
