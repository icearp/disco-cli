package aws

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/serverlessapplicationrepository"
	sartypes "github.com/aws/aws-sdk-go-v2/service/serverlessapplicationrepository/types"
	"github.com/icearp/disco-cli/store"
)

var sarDenied = apiErr("ForbiddenException", "User: arn:aws:iam::123:user/x is not authorized to perform: serverlessrepo:List")

func sarAppARN(name string) string {
	return "arn:aws:serverlessrepo:us-east-1:" + testAccountID + ":applications/" + name
}

// sarApps stores an application row per name and returns them as the child
// phases' parents.
func sarApps(t *testing.T, st *store.Store, names ...string) []childParent {
	t.Helper()
	apps := make([]childParent, len(names))
	for i, name := range names {
		apps[i] = childParent{id: sarAppARN(name), arn: sarAppARN(name)}
		upsertTestResource(t, st, "aws", testAccountID, TypeServerlessRepoApplication, apps[i].arn, testRegion, "{}")
	}
	return apps
}

func newSARTestClient(handlers map[string]func(in any) (any, error)) *serverlessapplicationrepository.Client {
	return serverlessapplicationrepository.NewFromConfig(opStubConfig(handlers, sarDenied))
}

func sarVersion(semver string) sartypes.VersionSummary {
	return sartypes.VersionSummary{SemanticVersion: sdkaws.String(semver), CreationTime: sdkaws.String("2026-01-02T03:04:05.000Z")}
}

// sarVersionsHandler answers ListApplicationVersions from pages, keyed by
// application ARN; an application with two pages returns the second for
// token "p2". errs fails an application's call, or its second page when keyed
// "<arn>/p2". It records each application queried and every MaxItems sent.
func sarVersionsHandler(calls *parentCalls, maxItems *parentCalls, pages map[string][][]sartypes.VersionSummary, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*serverlessapplicationrepository.ListApplicationVersionsInput)
		arn := sdkaws.ToString(req.ApplicationId)
		calls.add(arn)
		maxItems.add(fmt.Sprint(sdkaws.ToInt32(req.MaxItems)))
		page := 0
		if sdkaws.ToString(req.NextToken) == "p2" {
			page = 1
			if err := errs[arn+"/p2"]; err != nil {
				return nil, err
			}
		} else if err := errs[arn]; err != nil {
			return nil, err
		}
		out := &serverlessapplicationrepository.ListApplicationVersionsOutput{}
		if page < len(pages[arn]) {
			out.Versions = pages[arn][page]
		}
		if page == 0 && len(pages[arn]) > 1 {
			out.NextToken = sdkaws.String("p2")
		}
		return out, nil
	}
}

func sarStatement(id string) sartypes.ApplicationPolicyStatement {
	return sartypes.ApplicationPolicyStatement{
		StatementId: sdkaws.String(id), Actions: []string{"Deploy"},
		Principals: []string{"111122223333"}, PrincipalOrgIDs: []string{"o-abc123"},
	}
}

// sarPolicyHandler answers GetApplicationPolicy from statements keyed by
// application ARN; errs fails an application's call.
func sarPolicyHandler(calls *parentCalls, statements map[string][]sartypes.ApplicationPolicyStatement, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		arn := sdkaws.ToString(in.(*serverlessapplicationrepository.GetApplicationPolicyInput).ApplicationId)
		calls.add(arn)
		if err := errs[arn]; err != nil {
			return nil, err
		}
		return &serverlessapplicationrepository.GetApplicationPolicyOutput{Statements: statements[arn]}, nil
	}
}

func TestScanServerlessRepoApplicationVersions_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	first := sarVersion("1.0.0")
	first.SourceCodeUrl = sdkaws.String("https://example.com/src")
	var calls, maxItems parentCalls
	client := newSARTestClient(map[string]func(any) (any, error){
		"ListApplicationVersions": sarVersionsHandler(&calls, &maxItems, map[string][][]sartypes.VersionSummary{
			sarAppARN("app1"): {{first, {}}, {sarVersion("1.1.0")}},
			sarAppARN("app2"): {{sarVersion("2.0.0")}},
		}, nil),
	})

	total, _, err := scanServerlessRepoApplicationVersions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, sarApps(t, st, "app1", "app2", "app3"))
	if err != nil {
		t.Fatalf("scanServerlessRepoApplicationVersions: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	if got, want := calls.distinct(), []string{sarAppARN("app1"), sarAppARN("app2"), sarAppARN("app3")}; !reflect.DeepEqual(got, want) {
		t.Errorf("applications queried = %v; want %v", got, want)
	}
	if got := maxItems.distinct(); !reflect.DeepEqual(got, []string{"100"}) {
		t.Errorf("MaxItems sent = %v; want [100]", got)
	}
	rows := rowsByNativeID(t, st, TypeServerlessRepoApplicationVersion)
	if len(rows) != 3 {
		t.Fatalf("rows = %d; want 3 (the version-less element skipped): %v", len(rows), rows)
	}
	firstID := sarAppARN("app1") + "/version/1.0.0"
	for _, want := range []struct{ app, id string }{
		{sarAppARN("app1"), firstID},
		{sarAppARN("app1"), sarAppARN("app1") + "/version/1.1.0"},
		{sarAppARN("app2"), sarAppARN("app2") + "/version/2.0.0"},
	} {
		r, ok := rows[want.id]
		if !ok {
			t.Errorf("missing version %s", want.id)
			continue
		}
		if r.Type != TypeServerlessRepoApplicationVersion || sv(r.Region) != testRegion {
			t.Errorf("%s: Type %q Region %q", want.id, r.Type, sv(r.Region))
		}
		assertContainsNative(t, st, want.app, want.id)
	}
	r := rows[firstID]
	if sv(r.Name) != "1.0.0" || sv(r.CreatedAt) != "2026-01-02T03:04:05.000Z" {
		t.Errorf("Name, CreatedAt = %q, %q; want 1.0.0, 2026-01-02T03:04:05.000Z", sv(r.Name), sv(r.CreatedAt))
	}
	if !strings.Contains(r.AttributesJSON, `"SourceCodeUrl":"https://example.com/src"`) {
		t.Errorf("attrs = %s; want SourceCodeUrl", r.AttributesJSON)
	}
}

func TestScanServerlessRepoApplicationVersions_Empty(t *testing.T) {
	st := newTestStore(t)
	var calls, maxItems parentCalls
	client := newSARTestClient(map[string]func(any) (any, error){
		"ListApplicationVersions": sarVersionsHandler(&calls, &maxItems, nil, nil),
	})
	total, _, err := scanServerlessRepoApplicationVersions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, sarApps(t, st, "app1"))
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeServerlessRepoApplicationVersion)); n != 0 {
		t.Errorf("rows = %d; want 0", n)
	}
}

// sarPerAppErrorCases are the per-application outcomes both child phases
// share. Two of three applications fail, so a skip that warns must warn once
// for both.
var sarPerAppErrorCases = []struct {
	name      string
	err       error
	wantErr   string
	wantWarns int
}{
	{name: "denied warns once", err: sarDenied, wantWarns: 1},
	{name: "throttled warns once", err: apiErr("TooManyRequestsException", "Rate exceeded"), wantWarns: 1},
	{name: "application deleted since listing skipped silently", err: apiErr("NotFoundException", "gone")},
	{name: "other code fails the phase", err: apiErr("BadRequestException", "bad"), wantErr: "BadRequestException"},
}

func TestScanServerlessRepoApplicationVersions_PerApplicationErrors(t *testing.T) {
	for _, tc := range sarPerAppErrorCases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, sarListApplicationVersionsOp)
			var calls, maxItems parentCalls
			client := newSARTestClient(map[string]func(any) (any, error){
				"ListApplicationVersions": sarVersionsHandler(&calls, &maxItems, map[string][][]sartypes.VersionSummary{
					sarAppARN("app3"): {{sarVersion("3.0.0")}},
				}, map[string]error{sarAppARN("app1"): tc.err, sarAppARN("app2"): tc.err}),
			})
			total, _, err := scanServerlessRepoApplicationVersions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, sarApps(t, st, "app1", "app2", "app3"))
			if tc.wantErr != "" {
				if !isAPIErrorCode(err, tc.wantErr) || classifyServiceError(err) != outcomeError {
					t.Errorf("err = %v; want a hard %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			if _, ok := rowsByNativeID(t, st, TypeServerlessRepoApplicationVersion)[sarAppARN("app3")+"/version/3.0.0"]; !ok || total != 1 {
				t.Errorf("total = %d, sibling row stored = %v; want 1, true", total, ok)
			}
		})
	}
}

// An application whose second page of versions fails transiently is skipped
// whole, its first-page versions included, with one warning; its sibling's
// versions are stored.
func TestScanServerlessRepoApplicationVersions_SecondPageFailureDropsApplication(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, sarListApplicationVersionsOp)
	var calls, maxItems parentCalls
	client := newSARTestClient(map[string]func(any) (any, error){
		"ListApplicationVersions": sarVersionsHandler(&calls, &maxItems, map[string][][]sartypes.VersionSummary{
			sarAppARN("app1"): {{sarVersion("1.0.0")}, {sarVersion("1.1.0")}},
			sarAppARN("app2"): {{sarVersion("2.0.0")}},
		}, map[string]error{sarAppARN("app1") + "/p2": apiErr("TooManyRequestsException", "Rate exceeded")}),
	})
	total, _, err := scanServerlessRepoApplicationVersions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, sarApps(t, st, "app1", "app2"))
	if err != nil || *warns != 1 {
		t.Fatalf("err, warnings = %v, %d; want nil, 1", err, *warns)
	}
	rows := rowsByNativeID(t, st, TypeServerlessRepoApplicationVersion)
	if _, ok := rows[sarAppARN("app2")+"/version/2.0.0"]; !ok || total != 1 || len(rows) != 1 {
		t.Errorf("total = %d, rows = %v; want only app2's 2.0.0", total, rows)
	}
}

// The listing stops when the service repeats the token it was sent, rather
// than looping on it.
func TestScanServerlessRepoApplicationVersions_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var n atomic.Int64
	client := newSARTestClient(map[string]func(any) (any, error){
		"ListApplicationVersions": func(any) (any, error) {
			if n.Add(1) > 5 {
				return nil, apiErr("BadRequestException", "looped")
			}
			return &serverlessapplicationrepository.ListApplicationVersionsOutput{NextToken: sdkaws.String("same")}, nil
		},
	})
	_, _, err := scanServerlessRepoApplicationVersions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, sarApps(t, st, "app1"))
	if err != nil || n.Load() != 2 {
		t.Errorf("err, calls = %v, %d; want nil, 2", err, n.Load())
	}
}

// An application whose policy has statements gets one policy row under the
// application; one with no statements gets none.
func TestScanServerlessRepoApplicationPolicies_StoredAndEmptySkipped(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newSARTestClient(map[string]func(any) (any, error){
		"GetApplicationPolicy": sarPolicyHandler(&calls, map[string][]sartypes.ApplicationPolicyStatement{
			sarAppARN("app1"): {sarStatement("s1"), sarStatement("s2")},
			sarAppARN("app3"): {sarStatement("s3")},
		}, nil),
	})

	total, _, err := scanServerlessRepoApplicationPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, sarApps(t, st, "app1", "app2", "app3"))
	if err != nil {
		t.Fatalf("scanServerlessRepoApplicationPolicies: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
	if got, want := calls.distinct(), []string{sarAppARN("app1"), sarAppARN("app2"), sarAppARN("app3")}; !reflect.DeepEqual(got, want) {
		t.Errorf("applications queried = %v; want %v", got, want)
	}
	rows := rowsByNativeID(t, st, TypeServerlessRepoApplicationPolicy)
	if len(rows) != 2 {
		t.Fatalf("rows = %d; want 2 (app2 has no statements): %v", len(rows), rows)
	}
	for _, app := range []string{"app1", "app3"} {
		id := sarAppARN(app) + "/policy"
		r, ok := rows[id]
		if !ok {
			t.Errorf("missing policy %s", id)
			continue
		}
		if r.Type != TypeServerlessRepoApplicationPolicy || sv(r.Region) != testRegion || sv(r.Name) != "policy" || r.ManagedByProvider {
			t.Errorf("%s: Type %q Region %q Name %q Managed %v; want %s, %s, policy, false",
				id, r.Type, sv(r.Region), sv(r.Name), r.ManagedByProvider, TypeServerlessRepoApplicationPolicy, testRegion)
		}
		assertContainsNative(t, st, sarAppARN(app), id)
	}
	attrs := rows[sarAppARN("app1")+"/policy"].AttributesJSON
	for _, want := range []string{`"StatementId":"s2"`, `"Principals":["111122223333"]`, `"PrincipalOrgIDs":["o-abc123"]`} {
		if !strings.Contains(attrs, want) {
			t.Errorf("attrs = %s; want %s", attrs, want)
		}
	}
}

func TestScanServerlessRepoApplicationPolicies_PerApplicationErrors(t *testing.T) {
	for _, tc := range sarPerAppErrorCases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, sarGetApplicationPolicyOp)
			var calls parentCalls
			client := newSARTestClient(map[string]func(any) (any, error){
				"GetApplicationPolicy": sarPolicyHandler(&calls, map[string][]sartypes.ApplicationPolicyStatement{
					sarAppARN("app3"): {sarStatement("s3")},
				}, map[string]error{sarAppARN("app1"): tc.err, sarAppARN("app2"): tc.err}),
			})
			total, _, err := scanServerlessRepoApplicationPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, sarApps(t, st, "app1", "app2", "app3"))
			if tc.wantErr != "" {
				if !isAPIErrorCode(err, tc.wantErr) || classifyServiceError(err) != outcomeError {
					t.Errorf("err = %v; want a hard %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			if _, ok := rowsByNativeID(t, st, TypeServerlessRepoApplicationPolicy)[sarAppARN("app3")+"/policy"]; !ok || total != 1 {
				t.Errorf("total = %d, sibling row stored = %v; want 1, true", total, ok)
			}
		})
	}
}

// sarTopLevelHandlers answers the ops scanServerlessRepoAll calls: two pages
// of applications (app1, then app2 and an id-less element), each with one
// version and one policy statement. The child handlers fail unless the
// application row is already stored, since the child phases fan out over
// stored applications only.
func sarTopLevelHandlers(st *store.Store, versionCalls, policyCalls *parentCalls) map[string]func(any) (any, error) {
	stored := func(arn string) error {
		if _, err := st.GetResource(store.ResourceID("aws", testAccountID, arn)); err != nil {
			return fmt.Errorf("application %s not stored before its children: %w", arn, err)
		}
		return nil
	}
	return map[string]func(any) (any, error){
		"ListApplications": func(in any) (any, error) {
			if sdkaws.ToString(in.(*serverlessapplicationrepository.ListApplicationsInput).NextToken) == "p2" {
				return &serverlessapplicationrepository.ListApplicationsOutput{Applications: []sartypes.ApplicationSummary{
					{ApplicationId: sdkaws.String(sarAppARN("app2")), Name: sdkaws.String("app2")},
					{Name: sdkaws.String("no-id")},
				}}, nil
			}
			return &serverlessapplicationrepository.ListApplicationsOutput{
				Applications: []sartypes.ApplicationSummary{{ApplicationId: sdkaws.String(sarAppARN("app1")), Name: sdkaws.String("app1")}},
				NextToken:    sdkaws.String("p2"),
			}, nil
		},
		"ListApplicationVersions": func(in any) (any, error) {
			arn := sdkaws.ToString(in.(*serverlessapplicationrepository.ListApplicationVersionsInput).ApplicationId)
			if err := stored(arn); err != nil {
				return nil, err
			}
			versionCalls.add(arn)
			return &serverlessapplicationrepository.ListApplicationVersionsOutput{Versions: []sartypes.VersionSummary{sarVersion("1.0.0")}}, nil
		},
		"GetApplicationPolicy": func(in any) (any, error) {
			arn := sdkaws.ToString(in.(*serverlessapplicationrepository.GetApplicationPolicyInput).ApplicationId)
			if err := stored(arn); err != nil {
				return nil, err
			}
			policyCalls.add(arn)
			return &serverlessapplicationrepository.GetApplicationPolicyOutput{Statements: []sartypes.ApplicationPolicyStatement{sarStatement("s1")}}, nil
		},
	}
}

// The top-level scan stores both pages of applications, then the version and
// policy of every stored application, each contained by its application.
func TestScanServerlessRepoAll_ChildrenOfStoredApplications(t *testing.T) {
	st := newTestStore(t)
	var versionCalls, policyCalls parentCalls
	client := newSARTestClient(sarTopLevelHandlers(st, &versionCalls, &policyCalls))

	total, _, err := scanServerlessRepoAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 6 {
		t.Errorf("total = %d; want 6 (2 applications + 2 versions + 2 policies)", total)
	}
	want := []string{sarAppARN("app1"), sarAppARN("app2")}
	if got := versionCalls.distinct(); !reflect.DeepEqual(got, want) {
		t.Errorf("applications queried for versions = %v; want %v", got, want)
	}
	if got := policyCalls.distinct(); !reflect.DeepEqual(got, want) {
		t.Errorf("applications queried for policies = %v; want %v", got, want)
	}
	if n := len(rowsByNativeID(t, st, TypeServerlessRepoApplication)); n != 2 {
		t.Errorf("application rows = %d; want 2", n)
	}
	for _, app := range want {
		assertContainsNative(t, st, app, app+"/version/1.0.0")
		assertContainsNative(t, st, app, app+"/policy")
	}
}

// failSARListApplicationsPage makes the top-level handlers' ListApplications
// fail on the given page (1 or 2) with err; page 1 otherwise lists app1.
func failSARListApplicationsPage(h map[string]func(any) (any, error), page int, err error) {
	list := h["ListApplications"]
	h["ListApplications"] = func(in any) (any, error) {
		onPage2 := sdkaws.ToString(in.(*serverlessapplicationrepository.ListApplicationsInput).NextToken) == "p2"
		if onPage2 == (page == 2) {
			return nil, err
		}
		return list(in)
	}
}

// A ListApplications page that fails keeps the applications listed before
// it: they are stored and their versions and policies listed. A denial warns
// and is dropped; any other error is returned. A first-page failure lists no
// application, so no child call is made.
func TestScanServerlessRepoAll_ApplicationPageError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		page      int
		err       error
		wantErr   string
		wantWarns int
		wantApps  []string
	}{
		{name: "page 2 denied warns", page: 2, err: sarDenied, wantWarns: 1, wantApps: []string{sarAppARN("app1")}},
		{name: "page 2 other error returned", page: 2, err: apiErr("BadRequestException", "bad"), wantErr: "BadRequestException", wantApps: []string{sarAppARN("app1")}},
		{name: "page 1 denied warns", page: 1, err: sarDenied, wantWarns: 1},
		{name: "page 1 other error returned", page: 1, err: apiErr("BadRequestException", "bad"), wantErr: "BadRequestException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, sarListApplicationsOp)
			var versionCalls, policyCalls parentCalls
			h := sarTopLevelHandlers(st, &versionCalls, &policyCalls)
			failSARListApplicationsPage(h, tc.page, tc.err)
			client := newSARTestClient(h)

			total, _, err := scanServerlessRepoAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr != "" {
				if !isAPIErrorCode(err, tc.wantErr) || classifyServiceError(err) != outcomeError {
					t.Errorf("err = %v; want a hard %s", err, tc.wantErr)
				}
			} else if err != nil {
				t.Errorf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			if want := 3 * len(tc.wantApps); total != want {
				t.Errorf("total = %d; want %d (each application with its version and policy)", total, want)
			}
			if n := len(rowsByNativeID(t, st, TypeServerlessRepoApplication)); n != len(tc.wantApps) {
				t.Errorf("application rows = %d; want %d", n, len(tc.wantApps))
			}
			if got := versionCalls.distinct(); !reflect.DeepEqual(got, tc.wantApps) {
				t.Errorf("applications queried for versions = %v; want %v", got, tc.wantApps)
			}
			if got := policyCalls.distinct(); !reflect.DeepEqual(got, tc.wantApps) {
				t.Errorf("applications queried for policies = %v; want %v", got, tc.wantApps)
			}
			for _, app := range tc.wantApps {
				assertContainsNative(t, st, app, app+"/version/1.0.0")
				assertContainsNative(t, st, app, app+"/policy")
			}
		})
	}
}

// The application listing stops when the service repeats the token it was
// sent, rather than looping on it.
func TestScanServerlessRepoApplications_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var n atomic.Int64
	client := newSARTestClient(map[string]func(any) (any, error){
		"ListApplications": func(any) (any, error) {
			if n.Add(1) > 5 {
				return nil, apiErr("BadRequestException", "looped")
			}
			return &serverlessapplicationrepository.ListApplicationsOutput{NextToken: sdkaws.String("same")}, nil
		},
	})
	_, _, _, err := scanServerlessRepoApplications(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || n.Load() != 2 {
		t.Errorf("err, calls = %v, %d; want nil, 2", err, n.Load())
	}
}

// A hard error in the version phase does not stop the policy phase, and both
// phases' errors reach the dispatcher as a hard error with every stored row
// counted.
func TestScanServerlessRepoAll_ChildPhasesIndependent(t *testing.T) {
	for _, failing := range []string{"ListApplicationVersions", "GetApplicationPolicy"} {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			var versionCalls, policyCalls parentCalls
			h := sarTopLevelHandlers(st, &versionCalls, &policyCalls)
			h[failing] = func(any) (any, error) { return nil, apiErr("BadRequestException", "bad") }
			client := newSARTestClient(h)

			total, _, err := scanServerlessRepoAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if !isAPIErrorCode(err, "BadRequestException") || classifyServiceError(err) != outcomeError {
				t.Errorf("err = %v; want a hard BadRequestException", err)
			}
			if total != 4 {
				t.Errorf("total = %d; want 4 (2 applications + the other phase's 2 rows)", total)
			}
		})
	}
}

// A throttled child phase is demoted to one warning, so the other child
// phase's hard error still reaches the dispatcher as a hard error.
func TestScanServerlessRepoAll_TransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, sarListApplicationVersionsOp)
	var versionCalls, policyCalls parentCalls
	h := sarTopLevelHandlers(st, &versionCalls, &policyCalls)
	h["ListApplicationVersions"] = func(any) (any, error) { return nil, apiErr("TooManyRequestsException", "slow down") }
	h["GetApplicationPolicy"] = func(any) (any, error) { return nil, apiErr("BadRequestException", "bad") }
	client := newSARTestClient(h)

	_, _, err := scanServerlessRepoAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !isAPIErrorCode(err, "BadRequestException") {
		t.Errorf("err = %v; want the policy phase's BadRequestException", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the skipped throttled applications", *warns)
	}
}

// A throttled application listing is demoted to one warning, so a child
// phase's hard error still reaches the dispatcher as a hard error, and the
// applications listed before the throttle stay stored.
func TestScanServerlessRepoAll_ApplicationTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, sarListApplicationsOp)
	var versionCalls, policyCalls parentCalls
	h := sarTopLevelHandlers(st, &versionCalls, &policyCalls)
	failSARListApplicationsPage(h, 2, apiErr("TooManyRequestsException", "slow down"))
	h["GetApplicationPolicy"] = func(any) (any, error) { return nil, apiErr("BadRequestException", "bad") }
	client := newSARTestClient(h)

	total, _, err := scanServerlessRepoAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !isAPIErrorCode(err, "BadRequestException") || classifyServiceError(err) != outcomeError {
		t.Errorf("err = %v; want a hard BadRequestException", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the demoted throttle", *warns)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (app1 and its version)", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeServerlessRepoApplication)[sarAppARN("app1")]; !ok {
		t.Error("app1, listed before the throttle, not stored")
	}
	assertContainsNative(t, st, sarAppARN("app1"), sarAppARN("app1")+"/version/1.0.0")
}

// sarClosedStore returns a store whose every write fails. Not newTestStore:
// its cleanup queries the DB, which a closed store can't serve.
func sarClosedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return st
}

// A failed application write may have left the listed applications unstored,
// so no child phase runs for them, and the write error reaches the dispatcher
// as a store-write failure, even when a later page was throttled (a throttle
// alone would be demoted to a warning).
func TestScanServerlessRepoAll_ApplicationWriteErrorRunsNoChildPhase(t *testing.T) {
	st := sarClosedStore(t)
	var childCalls parentCalls
	h := sarTopLevelHandlers(st, nil, nil)
	h["ListApplicationVersions"] = func(in any) (any, error) {
		childCalls.add("ListApplicationVersions " + sdkaws.ToString(in.(*serverlessapplicationrepository.ListApplicationVersionsInput).ApplicationId))
		return &serverlessapplicationrepository.ListApplicationVersionsOutput{}, nil
	}
	h["GetApplicationPolicy"] = func(in any) (any, error) {
		childCalls.add("GetApplicationPolicy " + sdkaws.ToString(in.(*serverlessapplicationrepository.GetApplicationPolicyInput).ApplicationId))
		return &serverlessapplicationrepository.GetApplicationPolicyOutput{}, nil
	}
	client := newSARTestClient(h)

	_, _, err := scanServerlessRepoAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) || classifyServiceError(err) != outcomeStoreWrite {
		t.Errorf("err = %v (outcome %v); want store.ErrStoreWrite classified outcomeStoreWrite", err, classifyServiceError(err))
	}
	if got := childCalls.distinct(); len(got) != 0 {
		t.Errorf("child calls after the application write failed = %v; want none", got)
	}
}
