package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/amplify"
	amplifytypes "github.com/aws/aws-sdk-go-v2/service/amplify/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var (
	ampUnauthorized = &smithy.GenericAPIError{Code: "UnauthorizedException", Message: "Unauthorized"}
	ampDenied       = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: amplify:List"}
	ampBadRequest   = &smithy.GenericAPIError{Code: "BadRequestException", Message: "bad"}
	ampNotFound     = &smithy.GenericAPIError{Code: "NotFoundException", Message: "gone"}
	ampThrottled    = apiErr("ThrottlingException", "Rate exceeded")
)

var (
	ampTags    = map[string]string{"team": "web"}
	ampCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

const ampCreatedRFC3339 = "2026-01-02T03:04:05Z"

func ampAppARN(app string) string {
	return fmt.Sprintf("arn:aws:amplify:%s:%s:apps/%s", testRegion, testAccountID, app)
}

func ampBranchARN(app, branch string) string { return ampAppARN(app) + "/branches/" + branch }

func ampDomainARN(app, domain string) string { return ampAppARN(app) + "/domains/" + domain }

func ampWebhookARN(id string) string {
	return fmt.Sprintf("arn:aws:amplify:%s:%s:webhooks/%s", testRegion, testAccountID, id)
}

// ampFixture is what the stub lists: the app list and app a's listings have
// two pages, and each listing holds an element with no ARN. App a and its
// main branch are tagged and carry basic-auth credentials and environment
// variables; the other elements carry neither. The app with ARN
// ampAppARN("noid") has no AppId.
type ampFixture struct {
	apps     [][]amplifytypes.App
	branches map[string][][]amplifytypes.Branch            // by AppId
	domains  map[string][][]amplifytypes.DomainAssociation // by AppId
	webhooks map[string][][]amplifytypes.Webhook           // by AppId
}

func ampSecretEnv() map[string]string {
	return map[string]string{"DB_PASSWORD": "hunter2", "FEATURE_X": "true"}
}

func newAmpFixture() ampFixture {
	app := func(id string, tags map[string]string) amplifytypes.App {
		return amplifytypes.App{AppArn: ptrStr(ampAppARN(id)), AppId: ptrStr(id), Name: ptrStr("site-" + id), CreateTime: &ampCreated, Tags: tags}
	}
	appA := app("a", ampTags)
	appA.BasicAuthCredentials = ptrStr("dXNlcjpwYXNz")
	appA.EnvironmentVariables = ampSecretEnv()
	branch := func(app, name string, tags map[string]string) amplifytypes.Branch {
		return amplifytypes.Branch{BranchArn: ptrStr(ampBranchARN(app, name)), BranchName: ptrStr(name), CreateTime: &ampCreated, Tags: tags}
	}
	mainA := branch("a", "main", ampTags)
	mainA.BasicAuthCredentials = ptrStr("dXNlcjpwYXNz")
	mainA.EnvironmentVariables = ampSecretEnv()
	domain := func(app, name string) amplifytypes.DomainAssociation {
		return amplifytypes.DomainAssociation{DomainAssociationArn: ptrStr(ampDomainARN(app, name)), DomainName: ptrStr(name), DomainStatus: amplifytypes.DomainStatusAvailable}
	}
	webhook := func(app, id string) amplifytypes.Webhook {
		return amplifytypes.Webhook{
			WebhookArn: ptrStr(ampWebhookARN(id)), WebhookId: ptrStr(id), AppId: ptrStr(app), BranchName: ptrStr("main"), CreateTime: &ampCreated,
			WebhookUrl: ptrStr("https://webhooks.amplify.us-east-1.amazonaws.com/prod/webhooks?id=" + id + "&token=SECRET"),
		}
	}
	return ampFixture{
		apps: [][]amplifytypes.App{
			{appA},
			{app("b", nil), {AppId: ptrStr("app-noarn")}, {AppArn: ptrStr(ampAppARN("noid"))}},
		},
		branches: map[string][][]amplifytypes.Branch{
			"a": {{mainA}, {branch("a", "dev", nil), {BranchName: ptrStr("noarn")}}},
			"b": {{branch("b", "main", nil)}},
		},
		domains: map[string][][]amplifytypes.DomainAssociation{
			"a": {{domain("a", "example.com")}, {domain("a", "example.org"), {DomainName: ptrStr("noarn.com")}}},
			"b": {{domain("b", "b.example.com")}},
		},
		webhooks: map[string][][]amplifytypes.Webhook{
			"a": {{webhook("a", "wh-1")}, {webhook("a", "wh-2"), {WebhookId: ptrStr("wh-noarn")}}},
			"b": {{webhook("b", "wh-3")}},
		},
	}
}

// ampPinned fails a call that does not ask for the model's page cap: a
// smaller page would only cost calls, but the cap is what the scanner sets.
func ampPinned(op string, got, want int32) error {
	if got != want {
		return fmt.Errorf("%s MaxResults = %d, want %d", op, got, want)
	}
	return nil
}

// ampHandlers serves f; fail is keyed by op, then by "<app>/<token>" (app ""
// for ListApps; token "" on page one). calls records each per-app call.
func ampHandlers(f ampFixture, fail map[string]map[string]error, calls map[string]*parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApps": func(in any) (any, error) {
			p := in.(*amplify.ListAppsInput)
			if err := ampPinned("ListApps", p.MaxResults, 100); err != nil {
				return nil, err
			}
			items, next, err := esServe(f.apps, "", p.NextToken, fail["ListApps"])
			if err != nil {
				return nil, err
			}
			return &amplify.ListAppsOutput{Apps: items, NextToken: next}, nil
		},
		"ListBranches": func(in any) (any, error) {
			p := in.(*amplify.ListBranchesInput)
			if err := ampPinned("ListBranches", p.MaxResults, 50); err != nil {
				return nil, err
			}
			app := sv(p.AppId)
			calls["ListBranches"].add(app)
			items, next, err := esServe(f.branches[app], app, p.NextToken, fail["ListBranches"])
			if err != nil {
				return nil, err
			}
			return &amplify.ListBranchesOutput{Branches: items, NextToken: next}, nil
		},
		"ListDomainAssociations": func(in any) (any, error) {
			p := in.(*amplify.ListDomainAssociationsInput)
			if err := ampPinned("ListDomainAssociations", p.MaxResults, 50); err != nil {
				return nil, err
			}
			app := sv(p.AppId)
			calls["ListDomainAssociations"].add(app)
			items, next, err := esServe(f.domains[app], app, p.NextToken, fail["ListDomainAssociations"])
			if err != nil {
				return nil, err
			}
			return &amplify.ListDomainAssociationsOutput{DomainAssociations: items, NextToken: next}, nil
		},
		"ListWebhooks": func(in any) (any, error) {
			p := in.(*amplify.ListWebhooksInput)
			if err := ampPinned("ListWebhooks", p.MaxResults, 50); err != nil {
				return nil, err
			}
			app := sv(p.AppId)
			calls["ListWebhooks"].add(app)
			items, next, err := esServe(f.webhooks[app], app, p.NextToken, fail["ListWebhooks"])
			if err != nil {
				return nil, err
			}
			return &amplify.ListWebhooksOutput{Webhooks: items, NextToken: next}, nil
		},
	}
}

func newAmpCalls() map[string]*parentCalls {
	return map[string]*parentCalls{"ListBranches": {}, "ListDomainAssociations": {}, "ListWebhooks": {}}
}

var ampOpLabels = map[string]string{
	"ListApps":               "amplify:ListApps",
	"ListBranches":           "amplify:ListBranches",
	"ListDomainAssociations": "amplify:ListDomainAssociations",
	"ListWebhooks":           "amplify:ListWebhooks",
}

// ampRun is what one scanAmplify run returned and warned.
type ampRun struct {
	total, inserted int
	err             error
	warnings        []store.ScanWarning
}

// ampScan runs scanAmplify under ctx with the fixture's handlers, each passed
// through wrap (nil for none).
func ampScan(ctx context.Context, st *store.Store, fail map[string]map[string]error, calls map[string]*parentCalls, wrap func(op string, h func(in any) (any, error)) func(in any) (any, error)) ampRun {
	var (
		mu  sync.Mutex
		run ampRun
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		run.warnings = append(run.warnings, w)
	}
	h := ampHandlers(newAmpFixture(), fail, calls)
	if wrap != nil {
		for op, fn := range h {
			h[op] = wrap(op, fn)
		}
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, ampDenied)
	run.total, run.inserted, run.err = scanAmplify(ctx, acct, testRegion, st, testScanID)
	return run
}

func ampScanTop(st *store.Store, fail map[string]map[string]error) ampRun {
	return ampScan(context.Background(), st, fail, newAmpCalls(), nil)
}

var ampTypes = []string{TypeAmplifyApp, TypeAmplifyBranch, TypeAmplifyDomain, TypeAmplifyWebhooks}

// ampAllRows is every NativeID the full fixture stores, per type.
var ampAllRows = map[string][]string{
	TypeAmplifyApp:      {ampAppARN("a"), ampAppARN("b"), ampAppARN("noid")},
	TypeAmplifyBranch:   {ampBranchARN("a", "main"), ampBranchARN("a", "dev"), ampBranchARN("b", "main")},
	TypeAmplifyDomain:   {ampDomainARN("a", "example.com"), ampDomainARN("a", "example.org"), ampDomainARN("b", "b.example.com")},
	TypeAmplifyWebhooks: {ampWebhookARN("wh-1"), ampWebhookARN("wh-2"), ampWebhookARN("wh-3")},
}

// ampWithout is ampAllRows with types typs, and the given rows, removed.
func ampWithout(typs []string, rows ...string) map[string][]string {
	out := map[string][]string{}
	for typ, ids := range ampAllRows {
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

// assertAmpRows checks the stored rows per type and the run's counts.
func assertAmpRows(t *testing.T, st *store.Store, run ampRun, want map[string][]string) {
	t.Helper()
	n := 0
	for _, typ := range ampTypes {
		got := nativeIDs(rowsByNativeID(t, st, typ))
		w := slices.Clone(want[typ])
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s rows = %v, want %v", typ, got, w)
		}
		n += len(w)
	}
	if run.total != n || run.inserted != n {
		t.Errorf("total, inserted = %d, %d; want %d, %d", run.total, run.inserted, n, n)
	}
}

// assertAmpWarnings checks op warned exactly once per key in keys, each
// warning naming its key.
func assertAmpWarnings(t *testing.T, run ampRun, op string, keys ...string) {
	t.Helper()
	var got []store.ScanWarning
	for _, w := range run.warnings {
		if w.Service == ampOpLabels[op] {
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

// containsAll reports whether s contains every one of subs.
func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// assertAmpRow checks a stored row's name, region, creation time ("" for
// none), attributes (compared decoded) and tags (nil: no tags stored).
func assertAmpRow(t *testing.T, row store.Resource, name, created string, attrs any, wantTags map[string]string) {
	t.Helper()
	if sv(row.Name) != name || sv(row.Region) != testRegion || sv(row.CreatedAt) != created || row.Status != nil {
		t.Errorf("%s: name/region/created/status = %q/%q/%q/%v; want %q/%q/%q/nil", row.NativeID, sv(row.Name), sv(row.Region), sv(row.CreatedAt), row.Status, name, testRegion, created)
	}
	var got, want any
	if err := json.Unmarshal([]byte(row.AttributesJSON), &got); err != nil {
		t.Fatalf("%s: decode attributes: %v", row.NativeID, err)
	}
	if err := json.Unmarshal([]byte(mustJSON(attrs)), &want); err != nil {
		t.Fatalf("decode want: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: attrs = %s, want %s", row.NativeID, row.AttributesJSON, mustJSON(attrs))
	}
	if wantTags == nil {
		if row.TagsJSON != nil {
			t.Errorf("%s: tags = %s, want none", row.NativeID, *row.TagsJSON)
		}
		return
	}
	assertTagsMap(t, row.TagsJSON, wantTags)
}

// assertAmpChildren checks the app contains exactly the children given.
func assertAmpChildren(t *testing.T, st *store.Store, appARN string, children ...string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, appARN), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := make([]string, 0, len(rels))
	for _, r := range rels {
		got = append(got, r.ToID)
	}
	want := make([]string, 0, len(children))
	for _, c := range children {
		want = append(want, store.ResourceID("aws", testAccountID, c))
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s contains %v, want %v", appARN, got, want)
	}
}

func TestScanAmplify_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	calls := newAmpCalls()
	run := ampScan(context.Background(), st, nil, calls, nil)
	if run.err != nil {
		t.Fatalf("scanAmplify: %v", run.err)
	}
	assertAmpRows(t, st, run, ampAllRows)

	f := newAmpFixture()
	redacted := map[string]string{"DB_PASSWORD": redact.PlaceholderKeyName, "FEATURE_X": "true"}
	appA := f.apps[0][0]
	appA.BasicAuthCredentials, appA.EnvironmentVariables = ptrStr(redact.Placeholder), redacted
	apps := rowsByNativeID(t, st, TypeAmplifyApp)
	assertAmpRow(t, apps[ampAppARN("a")], "site-a", ampCreatedRFC3339, appA, ampTags)
	assertAmpRow(t, apps[ampAppARN("b")], "site-b", ampCreatedRFC3339, f.apps[1][0], nil)
	assertAmpRow(t, apps[ampAppARN("noid")], "", "", f.apps[1][2], nil)

	mainA := f.branches["a"][0][0]
	mainA.BasicAuthCredentials, mainA.EnvironmentVariables = ptrStr(redact.Placeholder), redacted
	branches := rowsByNativeID(t, st, TypeAmplifyBranch)
	assertAmpRow(t, branches[ampBranchARN("a", "main")], "main", ampCreatedRFC3339, mainA, ampTags)
	assertAmpRow(t, branches[ampBranchARN("a", "dev")], "dev", ampCreatedRFC3339, f.branches["a"][1][0], nil)

	domains := rowsByNativeID(t, st, TypeAmplifyDomain)
	assertAmpRow(t, domains[ampDomainARN("a", "example.com")], "example.com", "", f.domains["a"][0][0], nil)
	wh1 := f.webhooks["a"][0][0]
	wh1.WebhookUrl = ptrStr(redact.Placeholder)
	webhooks := rowsByNativeID(t, st, TypeAmplifyWebhooks)
	assertAmpRow(t, webhooks[ampWebhookARN("wh-1")], "wh-1", ampCreatedRFC3339, wh1, nil)

	assertAmpChildren(t, st, ampAppARN("a"),
		ampBranchARN("a", "main"), ampBranchARN("a", "dev"), ampDomainARN("a", "example.com"), ampDomainARN("a", "example.org"),
		ampWebhookARN("wh-1"), ampWebhookARN("wh-2"))
	assertAmpChildren(t, st, ampAppARN("b"), ampBranchARN("b", "main"), ampDomainARN("b", "b.example.com"), ampWebhookARN("wh-3"))
	assertAmpChildren(t, st, ampAppARN("noid"))

	assertAmpWarnings(t, run, "ListApps", "app-noarn", ampAppARN("noid"))
	assertAmpWarnings(t, run, "ListBranches", ampAppARN("a")+"/branches/noarn")
	assertAmpWarnings(t, run, "ListDomainAssociations", ampAppARN("a")+"/domains/noarn.com")
	assertAmpWarnings(t, run, "ListWebhooks", ampAppARN("a")+"/webhooks/wh-noarn")
	for op, c := range calls {
		if got, want := c.distinct(), []string{"a", "b"}; !slices.Equal(got, want) {
			t.Errorf("%s called for %v, want %v", op, got, want)
		}
	}
}

// A failed page two keeps the rows read before it, the error is returned, and
// every later phase still runs over the apps stored.
func TestScanAmplify_FailedPageKeepsRowsAndRunsLaterPhases(t *testing.T) {
	for _, tc := range []struct {
		op, parent string
		want       map[string][]string
	}{
		{"ListApps", "", map[string][]string{
			TypeAmplifyApp:      {ampAppARN("a")},
			TypeAmplifyBranch:   {ampBranchARN("a", "main"), ampBranchARN("a", "dev")},
			TypeAmplifyDomain:   {ampDomainARN("a", "example.com"), ampDomainARN("a", "example.org")},
			TypeAmplifyWebhooks: {ampWebhookARN("wh-1"), ampWebhookARN("wh-2")},
		}},
		{"ListBranches", "a", ampWithout(nil, ampBranchARN("a", "dev"))},
		{"ListDomainAssociations", "a", ampWithout(nil, ampDomainARN("a", "example.org"))},
		{"ListWebhooks", "a", ampWithout(nil, ampWebhookARN("wh-2"))},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			run := ampScanTop(st, map[string]map[string]error{tc.op: {tc.parent + "/p2": ampBadRequest}})
			if classifyServiceError(run.err) != outcomeError || !errors.Is(run.err, ampBadRequest) {
				t.Fatalf("err = %v, want the BadRequestException", run.err)
			}
			assertAmpRows(t, st, run, tc.want)
		})
	}
}

// A denial warns once per op, naming the first app it lost when the op fans
// out over apps, and is no error; the other phases keep their rows.
// UnauthorizedException, the denial every op models, and the codes
// isAccessDenied matches count as one denial.
func TestScanAmplify_DeniedOpWarnsOnce(t *testing.T) {
	both := func(a, b error) map[string]error { return map[string]error{"a/": a, "b/": b} }
	for _, tc := range []struct {
		name, op string
		fail     map[string]error
		keys     []string // all named by the op's one warning
		want     map[string][]string
	}{
		{"apps unauthorized", "ListApps", map[string]error{"/": ampUnauthorized}, []string{"ListApps"}, map[string][]string{}},
		// A denial after page one names what the later pages held ("apps").
		{"apps unauthorized on page two", "ListApps", map[string]error{"/p2": ampUnauthorized}, []string{"ListApps", "apps"}, map[string][]string{
			TypeAmplifyApp:      {ampAppARN("a")},
			TypeAmplifyBranch:   {ampBranchARN("a", "main"), ampBranchARN("a", "dev")},
			TypeAmplifyDomain:   {ampDomainARN("a", "example.com"), ampDomainARN("a", "example.org")},
			TypeAmplifyWebhooks: {ampWebhookARN("wh-1"), ampWebhookARN("wh-2")},
		}},
		{"apps access denied", "ListApps", map[string]error{"/": ampDenied}, []string{"ListApps"}, map[string][]string{}},
		{"branches unauthorized then denied", "ListBranches", both(ampUnauthorized, ampDenied), []string{"ListBranches", "a"}, ampWithout([]string{TypeAmplifyBranch})},
		{"branches denied then unauthorized", "ListBranches", both(ampDenied, ampUnauthorized), []string{"ListBranches", "a"}, ampWithout([]string{TypeAmplifyBranch})},
		{"branches unauthorized on page two", "ListBranches", map[string]error{"a/p2": ampUnauthorized}, []string{"ListBranches", "a"}, ampWithout(nil, ampBranchARN("a", "dev"))},
		{"domains", "ListDomainAssociations", both(ampUnauthorized, ampUnauthorized), []string{"ListDomainAssociations", "a"}, ampWithout([]string{TypeAmplifyDomain})},
		{"webhooks", "ListWebhooks", both(ampUnauthorized, ampUnauthorized), []string{"ListWebhooks", "a"}, ampWithout([]string{TypeAmplifyWebhooks})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			run := ampScanTop(st, map[string]map[string]error{tc.op: tc.fail})
			if run.err != nil {
				t.Fatalf("err = %v, want nil", run.err)
			}
			// Every case loses the page holding the op's no-ARN element, so
			// the denial is the op's only warning.
			var got []string
			for _, w := range run.warnings {
				if w.Service == ampOpLabels[tc.op] {
					got = append(got, w.Message)
				}
			}
			if len(got) != 1 || !containsAll(got[0], tc.keys) {
				t.Errorf("%s warnings = %q, want one naming each of %q", tc.op, got, tc.keys)
			}
			assertAmpRows(t, st, run, tc.want)
		})
	}
}

// Any other per-app error, a throttle and a NotFoundException (which no
// per-app op models) included, is returned after the other rows are stored:
// the lost children have no listed row to keep in their place.
func TestScanAmplify_OtherErrorsAreReturned(t *testing.T) {
	for _, tc := range []struct {
		name, op, key string
		err           error
		want          serviceOutcome
		rows          map[string][]string
	}{
		{"branches not found", "ListBranches", "b/", ampNotFound, outcomeError, ampWithout(nil, ampBranchARN("b", "main"))},
		{"branches throttled", "ListBranches", "b/", ampThrottled, outcomeTransient, ampWithout(nil, ampBranchARN("b", "main"))},
		{"domains throttled", "ListDomainAssociations", "b/", ampThrottled, outcomeTransient, ampWithout(nil, ampDomainARN("b", "b.example.com"))},
		{"webhooks throttled", "ListWebhooks", "b/", ampThrottled, outcomeTransient, ampWithout(nil, ampWebhookARN("wh-3"))},
		{"apps throttled", "ListApps", "/", ampThrottled, outcomeTransient, map[string][]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			run := ampScanTop(st, map[string]map[string]error{tc.op: {tc.key: tc.err}})
			if got := classifyServiceError(run.err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", run.err, got, tc.want)
			}
			if !errors.Is(run.err, tc.err) {
				t.Errorf("err = %v, want it to wrap %v", run.err, tc.err)
			}
			assertAmpRows(t, st, run, tc.rows)
		})
	}
}

// A webhook listing whose service repeats the token it was sent ends with
// errRepeatedNextToken after the rows read are stored.
func TestScanAmplify_RepeatedWebhookTokenIsReturned(t *testing.T) {
	st := newTestStore(t)
	run := ampScan(context.Background(), st, nil, newAmpCalls(), func(op string, h func(in any) (any, error)) func(in any) (any, error) {
		return func(in any) (any, error) {
			out, err := h(in)
			if p, ok := in.(*amplify.ListWebhooksInput); ok && sv(p.AppId) == "a" && sv(p.NextToken) == "p2" {
				out.(*amplify.ListWebhooksOutput).NextToken = ptrStr("p2")
			}
			return out, err
		}
	})
	if !errors.Is(run.err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", run.err)
	}
	assertAmpRows(t, st, run, ampAllRows)
}

// A throttled phase alone leaves the scan transient; beside another phase's
// hard error it is warned about once and the scan reads as an error.
func TestScanAmplify_TransientPhaseBesideHardError(t *testing.T) {
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
			fail := map[string]map[string]error{"ListBranches": {"b/": ampThrottled}}
			if tc.hard {
				fail["ListWebhooks"] = map[string]error{"a/": ampBadRequest}
			}
			run := ampScanTop(st, fail)
			if got := classifyServiceError(run.err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", run.err, got, tc.want)
			}
			n := 0
			for _, w := range run.warnings {
				if w.Service == "amplify:ListBranches" && strings.Contains(w.Message, "ListBranches b") {
					n++
				}
			}
			if n != tc.wantWarns {
				t.Errorf("ListBranches warnings naming app b = %d, want %d", n, tc.wantWarns)
			}
		})
	}
}

// A failed write stops the scan with no warning claiming the lost rows, and
// no later phase runs; the rows of earlier phases stay stored.
func TestScanAmplify_WriteFailureStops(t *testing.T) {
	for _, tc := range []struct {
		typ, op string
		ops     []string
		kept    map[string][]string
	}{
		{TypeAmplifyApp, "ListApps", []string{"ListApps"}, map[string][]string{}},
		{TypeAmplifyBranch, "ListBranches", []string{"ListApps", "ListBranches"}, ampWithout([]string{TypeAmplifyBranch, TypeAmplifyDomain, TypeAmplifyWebhooks})},
		{TypeAmplifyDomain, "ListDomainAssociations", []string{"ListApps", "ListBranches", "ListDomainAssociations"}, ampWithout([]string{TypeAmplifyDomain, TypeAmplifyWebhooks}, ampBranchARN("b", "main"))},
		{TypeAmplifyWebhooks, "ListWebhooks", []string{"ListApps", "ListBranches", "ListDomainAssociations", "ListWebhooks"}, ampWithout([]string{TypeAmplifyWebhooks}, ampBranchARN("b", "main"))},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			st := newTestStore(t)
			refuseStoreWrites(t, st, tc.typ)
			var ops parentCalls
			// app b's branches are denied, so the branch phase has a denial
			// to warn about and must not once its write failed.
			fail := map[string]map[string]error{"ListBranches": {"b/": ampUnauthorized}}
			run := ampScan(context.Background(), st, fail, newAmpCalls(), func(op string, h func(in any) (any, error)) func(in any) (any, error) {
				return func(in any) (any, error) {
					ops.add(op)
					return h(in)
				}
			})
			if !errors.Is(run.err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want the store write failure", run.err)
			}
			assertAmpWarnings(t, run, tc.op)
			want := slices.Clone(tc.ops)
			slices.Sort(want)
			if got := ops.distinct(); !slices.Equal(got, want) {
				t.Errorf("ops called = %v; want %v", got, want)
			}
			for _, typ := range ampTypes {
				w := slices.Clone(tc.kept[typ])
				slices.Sort(w)
				if got := nativeIDs(rowsByNativeID(t, st, typ)); !slices.Equal(got, w) {
					t.Errorf("%s rows = %v, want %v", typ, got, w)
				}
			}
		})
	}
}

// A context ended after the branches were read stops the later phases, and
// the scan returns that stop.
func TestScanAmplify_ContextEndStopsLaterPhases(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu    sync.Mutex
		reads int
	)
	run := ampScan(ctx, st, nil, newAmpCalls(), func(op string, h func(in any) (any, error)) func(in any) (any, error) {
		return func(in any) (any, error) {
			if op == "ListDomainAssociations" || op == "ListWebhooks" {
				t.Errorf("%s called after the context ended", op)
			}
			out, err := h(in)
			if op == "ListBranches" {
				mu.Lock()
				defer mu.Unlock()
				// app a has two pages and app b one, so the third call is the last.
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
	assertAmpRows(t, st, run, ampWithout([]string{TypeAmplifyDomain, TypeAmplifyWebhooks}))
}
