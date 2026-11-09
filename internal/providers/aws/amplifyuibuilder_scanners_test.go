package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/amplify"
	amplifytypes "github.com/aws/aws-sdk-go-v2/service/amplify/types"
	"github.com/aws/aws-sdk-go-v2/service/amplifyuibuilder"
	auibtypes "github.com/aws/aws-sdk-go-v2/service/amplifyuibuilder/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

// The fixture: ListApps serves app dt on its first page and app db on its
// second; each app has one backend environment (auibEnvOf). Every
// per-environment listing serves its "t" entity on the first page and its "b"
// entity on the second. ListTagsForResource answers with tags for every ARN
// ending in "-t".

var auibEnvOf = map[string]string{"dt": "prod", "db": "dev"}

var (
	auibUnauthorized = &smithy.GenericAPIError{Code: "UnauthorizedException", Message: "You don't have permission to perform this operation."}
	auibBadInput     = &smithy.GenericAPIError{Code: "InvalidParameterException", Message: "bad"}
)

// auibKind is one entity type: its listing op, its type, its ARN segment and
// the prefix of its fixture ids.
type auibKind struct{ op, typ, seg, prefix string }

var auibKinds = []auibKind{
	{"ListComponents", TypeAmplifyUIBuilderComponent, "components", "c-"},
	{"ListForms", TypeAmplifyUIBuilderForm, "forms", "f-"},
	{"ListThemes", TypeAmplifyUIBuilderTheme, "themes", "th-"},
}

// auibTestARN is the Service Reference ARN of entity id of kind seg in app's
// backend environment env, built independently of auibARN.
func auibTestARN(partition, region, app, env, seg, id string) string {
	return "arn:" + partition + ":amplifyuibuilder:" + region + ":" + testAccountID + ":app/" + app + "/environment/" + env + "/" + seg + "/" + id
}

// auibElement is the element k's listing returns with id in app's
// environment env.
func auibElement(k auibKind, app, env, id string) any {
	name := sdkaws.String(k.seg + " " + id + " of " + app)
	switch k.op {
	case "ListComponents":
		return auibtypes.ComponentSummary{AppId: &app, EnvironmentName: &env, Id: &id, Name: name, ComponentType: sdkaws.String("Button")}
	case "ListForms":
		return auibtypes.FormSummary{
			AppId: &app, EnvironmentName: &env, Id: &id, Name: name, FormActionType: auibtypes.FormActionTypeCreate,
			DataType: &auibtypes.FormDataTypeConfig{DataSourceType: auibtypes.FormDataSourceTypeCustom, DataTypeName: sdkaws.String("Note")},
		}
	default:
		return auibtypes.ThemeSummary{AppId: &app, EnvironmentName: &env, Id: &id, Name: name}
	}
}

// auibPage is op's output holding elements, with next.
func auibPage(op string, next *string, elements ...any) any {
	switch op {
	case "ListComponents":
		out := &amplifyuibuilder.ListComponentsOutput{NextToken: next}
		for _, e := range elements {
			out.Entities = append(out.Entities, e.(auibtypes.ComponentSummary))
		}
		return out
	case "ListForms":
		out := &amplifyuibuilder.ListFormsOutput{NextToken: next}
		for _, e := range elements {
			out.Entities = append(out.Entities, e.(auibtypes.FormSummary))
		}
		return out
	default:
		out := &amplifyuibuilder.ListThemesOutput{NextToken: next}
		for _, e := range elements {
			out.Entities = append(out.Entities, e.(auibtypes.ThemeSummary))
		}
		return out
	}
}

// auibWant is a row the fixture stores: its type, ARN, Name and element.
type auibWant struct {
	typ, arn, name string
	element        any
}

// auibRows is every row the fixture stores.
func auibRows() []auibWant {
	var rows []auibWant
	for _, app := range []string{"dt", "db"} {
		env := auibEnvOf[app]
		for _, k := range auibKinds {
			for _, n := range []string{"t", "b"} {
				id := k.prefix + n
				el := auibElement(k, app, env, id)
				rows = append(rows, auibWant{k.typ, auibTestARN("aws", testRegion, app, env, k.seg, id), k.seg + " " + id + " of " + app, el})
			}
		}
	}
	return rows
}

// auibCalls records the stubbed calls and answers ListTagsForResource from
// tags and errs, keyed by ARN.
type auibCalls struct{ *bacCalls }

func newAUIBCalls() *auibCalls {
	c := &auibCalls{newBACCalls()}
	for _, r := range auibRows() {
		if strings.HasSuffix(r.arn, "-t") {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *auibCalls) listTags(in any) (any, error) {
	arn := sv(in.(*amplifyuibuilder.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &amplifyuibuilder.ListTagsForResourceOutput{Tags: tags}, nil
}

// calledAmplify records a call to an Amplify op, whose MaxResults is an
// int32 rather than the pointer bacCalls.called reads.
func (c *auibCalls) calledAmplify(op string, maxResults int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.maxResults[op] = append(c.maxResults[op], maxResults)
}

// auibEnvKey is the app and environment a per-environment listing was sent.
func auibEnvKey(in any) (app, env string) {
	return sv(bacField[*string](in, "AppId")), sv(bacField[*string](in, "EnvironmentName"))
}

// auibEntityPages answers k's listing with its "t" entity on the first page
// and its "b" entity on the second, in the environment it was sent.
func (c *auibCalls) auibEntityPages(k auibKind) func(any) (any, error) {
	return c.answer(k.op, func(in any) any {
		app, env := auibEnvKey(in)
		if bacField[*string](in, "NextToken") == nil {
			return auibPage(k.op, sdkaws.String("p2"), auibElement(k, app, env, k.prefix+"t"))
		}
		return auibPage(k.op, nil, auibElement(k, app, env, k.prefix+"b"))
	})
}

func auibApp(id string) amplifytypes.App {
	return amplifytypes.App{AppId: &id, Name: sdkaws.String("app " + id), AppArn: sdkaws.String(partitionARN("aws", "amplify", testRegion, "apps/"+id))}
}

// auibHandlers answers every op the scan calls with the fixture (auibRows).
func auibHandlers(c *auibCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListApps": func(in any) (any, error) {
			li := in.(*amplify.ListAppsInput)
			c.calledAmplify("ListApps", li.MaxResults)
			if li.NextToken == nil {
				return &amplify.ListAppsOutput{Apps: []amplifytypes.App{auibApp("dt")}, NextToken: sdkaws.String("p2")}, nil
			}
			return &amplify.ListAppsOutput{Apps: []amplifytypes.App{auibApp("db")}}, nil
		},
		"ListBackendEnvironments": func(in any) (any, error) {
			li := in.(*amplify.ListBackendEnvironmentsInput)
			c.calledAmplify("ListBackendEnvironments", li.MaxResults)
			return &amplify.ListBackendEnvironmentsOutput{BackendEnvironments: []amplifytypes.BackendEnvironment{{EnvironmentName: sdkaws.String(auibEnvOf[sv(li.AppId)])}}}, nil
		},
	}
	for _, k := range auibKinds {
		h[k.op] = c.auibEntityPages(k)
	}
	return h
}

// auibOpLabel is the label op's warnings and errors go under.
func auibOpLabel(op string) string {
	switch op {
	case "ListApps":
		return auibAppsOp
	case "ListBackendEnvironments":
		return auibEnvsOp
	}
	return "amplify-ui-builder:" + op
}

// auibScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func auibScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, crDenied)
	return scanAmplifyUIBuilder(ctx, acct, testRegion, st, testScanID)
}

func auibRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// auibComponentARN is the fixture ARN of app's component c-<n>.
func auibComponentARN(app, n string) string {
	return auibTestARN("aws", testRegion, app, auibEnvOf[app], "components", "c-"+n)
}

// Every type stores the tags ListTagsForResource returns for its ARN, nil for
// a resource without tags, and every stored row's ARN, built from the app
// and environment it was listed in, is sent to the tag op. Both pages of the
// app listing and of every entity listing are read, each listing asks for
// its model's page cap, and the scan reports no error or warning. Each row
// keeps the base scan's Name, Region and verbatim element, with no Status
// or CreatedAt.
func TestScanAmplifyUIBuilder_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newAUIBCalls()

	counted, upserts := countUpserts(st)
	total, err := auibScan(context.Background(), t, counted, auibHandlers(calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := auibRows()
	if total != 12 || inserted != 12 || len(rows) != 12 {
		t.Errorf("total, inserted = %d, %d; want 12 (fixture rows %d)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range rows {
		r, ok := auibRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		wantTagARNs = append(wantTagARNs, want.arn)
		if strings.HasSuffix(want.arn, "-t") {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %s; want nil", want.arn, *r.TagsJSON)
		}
		if sv(r.Name) != want.name {
			t.Errorf("%s Name = %v; want %q", want.arn, r.Name, want.name)
		}
		if r.Status != nil || r.CreatedAt != nil {
			t.Errorf("%s Status, CreatedAt = %v, %v; want nil, nil", want.arn, r.Status, r.CreatedAt)
		}
		if sv(r.Region) != testRegion || r.AttributesJSON != mustJSON(want.element) {
			t.Errorf("%s Region, attributes = %v, %s; want %s, %s", want.arn, r.Region, r.AttributesJSON, testRegion, mustJSON(want.element))
		}
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	for _, c := range []struct {
		op    string
		calls int
		max   int32
	}{
		{"ListApps", 2, 100},
		{"ListBackendEnvironments", 2, 50},
		{"ListComponents", 4, 100},
		{"ListForms", 4, 100},
		{"ListThemes", 4, 100},
	} {
		caps := calls.maxResults[c.op]
		if len(caps) != c.calls {
			t.Errorf("%s calls with MaxResults = %d; want %d", c.op, len(caps), c.calls)
		}
		for _, n := range caps {
			if n != c.max {
				t.Errorf("%s MaxResults = %d; want %d", c.op, n, c.max)
			}
		}
	}
}

// A cn or gov account scanned in its own region builds each ARN in its
// partition, sends it to the tag op unchanged and stores its tags.
func TestScanAmplifyUIBuilder_PartitionARNsReachTagOp(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newAUIBCalls()
			want := map[string]string{
				TypeAmplifyUIBuilderComponent: auibTestARN(pf.partition, pf.region, "db", "dev", "components", "c-t"),
				TypeAmplifyUIBuilderForm:      auibTestARN(pf.partition, pf.region, "db", "dev", "forms", "f-t"),
				TypeAmplifyUIBuilderTheme:     auibTestARN(pf.partition, pf.region, "db", "dev", "themes", "th-t"),
			}
			for typ, arn := range want {
				calls.tags[arn] = map[string]string{"env": typ}
			}
			h := auibHandlers(calls)
			h["ListApps"] = func(any) (any, error) {
				return &amplify.ListAppsOutput{Apps: []amplifytypes.App{{AppId: sdkaws.String("db")}}}, nil
			}
			for _, k := range auibKinds {
				h[k.op] = calls.answer(k.op, func(in any) any {
					app, env := auibEnvKey(in)
					return auibPage(k.op, nil, auibElement(k, app, env, k.prefix+"t"))
				})
			}
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, crDenied)

			total, err := scanAmplifyUIBuilder(context.Background(), acct, pf.region, st, testScanID)
			if err != nil || total != 3 {
				t.Fatalf("scan = %d, %v; want 3, nil", total, err)
			}
			got := slices.Clone(calls.tagARNs)
			slices.Sort(got)
			wantARNs := []string{want[TypeAmplifyUIBuilderComponent], want[TypeAmplifyUIBuilderForm], want[TypeAmplifyUIBuilderTheme]}
			slices.Sort(wantARNs)
			if !slices.Equal(got, wantARNs) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantARNs)
			}
			for typ, arn := range want {
				r, ok := auibRowOf(t, st, typ, arn)
				if !ok {
					t.Fatalf("%s %s not stored", typ, arn)
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"env": typ})
				if sv(r.Region) != pf.region {
					t.Errorf("%s Region = %v; want %s", arn, r.Region, pf.region)
				}
			}
		})
	}
}

// No apps, or apps whose environments list nothing, store nothing, read no
// tags and report nothing.
func TestScanAmplifyUIBuilder_EmptyListings(t *testing.T) {
	t.Parallel()
	t.Run("no apps", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls := newAUIBCalls()
		h := auibHandlers(calls)
		h["ListApps"] = func(any) (any, error) { return &amplify.ListAppsOutput{}, nil }

		counted, upserts := countUpserts(st)
		total, err := auibScan(context.Background(), t, counted, h)
		inserted := upserts()
		if err != nil || total != 0 || inserted != 0 {
			t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
		}
		if *warns != 0 || calls.count("ListBackendEnvironments") != 0 || calls.count("ListComponents") != 0 {
			t.Errorf("warnings, ListBackendEnvironments, ListComponents calls = %d, %d, %d; want 0, 0, 0", *warns, calls.count("ListBackendEnvironments"), calls.count("ListComponents"))
		}
	})
	t.Run("empty environments", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls := newAUIBCalls()
		h := auibHandlers(calls)
		for _, k := range auibKinds {
			h[k.op] = calls.answer(k.op, func(any) any { return auibPage(k.op, nil) })
		}

		total, err := auibScan(context.Background(), t, st, h)
		if err != nil || total != 0 {
			t.Fatalf("scan = %d, %v; want 0, nil", total, err)
		}
		for _, k := range auibKinds {
			if n := calls.count(k.op); n != 2 {
				t.Errorf("%s calls = %d; want 2, one per environment", k.op, n)
			}
		}
		if *warns != 0 || len(calls.tagARNs) != 0 {
			t.Errorf("warnings, tag reads = %d, %d; want 0, 0", *warns, len(calls.tagARNs))
		}
	})
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial (AccessDeniedException, or UnauthorizedException, the op's
// modeled denial) warns once under the tag op, a throttle after retries is a
// transient error, any other code a hard one; ResourceNotFoundException
// drops the row.
func TestScanAmplifyUIBuilder_TagReadFailures(t *testing.T) {
	t.Parallel()
	target := auibComponentARN("dt", "t")
	for _, c := range []struct {
		name      string
		err       error
		kept      bool
		outcome   serviceOutcome
		wantWarns int
	}{
		{"denied", crDenied, true, 0, 1},
		{"unauthorized", auibUnauthorized, true, 0, 1},
		{"not found", crNotFound, false, 0, 0},
		{"other error", auibBadInput, true, outcomeError, 0},
		{"transient", crThrottled, true, outcomeTransient, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, auibTagsOp)
			calls := newAUIBCalls()
			calls.errs[target] = c.err

			total, err := auibScan(context.Background(), t, st, auibHandlers(calls))
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			if c.outcome == outcomeError {
				assertAPICode(t, err, "InvalidParameterException")
			}
			r, ok := auibRowOf(t, st, TypeAmplifyUIBuilderComponent, target)
			if ok != c.kept {
				t.Fatalf("row stored = %v; want %v", ok, c.kept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			want := 12
			if !c.kept {
				want = 11
			}
			if total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if len(*warnings) != c.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", auibTagsOp, *warnings, c.wantWarns)
			}
			for _, w := range *warnings {
				if w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in several listings are one warning for the scan.
func TestScanAmplifyUIBuilder_TagDenialsWarnOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warnings := bacWarnings(st, auibTagsOp)
	calls := newAUIBCalls()
	theme := auibTestARN("aws", testRegion, "db", "dev", "themes", "th-b")
	calls.errs[auibComponentARN("dt", "t")] = crDenied
	calls.errs[theme] = auibUnauthorized

	if _, err := auibScan(context.Background(), t, st, auibHandlers(calls)); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("%s warnings = %v; want one, scoped %s/%s", auibTagsOp, *warnings, testAccountID, testRegion)
	}
	for _, r := range []struct{ typ, arn string }{
		{TypeAmplifyUIBuilderComponent, auibComponentARN("dt", "t")},
		{TypeAmplifyUIBuilderTheme, theme},
	} {
		if got, ok := auibRowOf(t, st, r.typ, r.arn); !ok || got.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", r.arn, ok, got.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanAmplifyUIBuilder_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, auibTagsOp)
	calls := newAUIBCalls()
	calls.errs[auibComponentARN("dt", "t")] = crThrottled
	calls.errs[auibTestARN("aws", testRegion, "db", "dev", "forms", "f-b")] = auibBadInput

	_, err := auibScan(context.Background(), t, st, auibHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", auibTagsOp, *warns)
	}
}

// A denied listing warns once under its own op and costs no other phase its
// rows: a denied app or environment listing (AccessDeniedException, or
// UnauthorizedException, which the Amplify ops model as their denial)
// leaves the entity listings no environment to read.
func TestScanAmplifyUIBuilder_DeniedListingWarnsUnderItsOp(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op   string
		err  error
		want int
	}{
		{"ListApps", crDenied, 0},
		{"ListApps", auibUnauthorized, 0},
		{"ListBackendEnvironments", crDenied, 0},
		{"ListBackendEnvironments", auibUnauthorized, 0},
		{"ListComponents", crDenied, 8},
		{"ListForms", crDenied, 8},
		{"ListThemes", crDenied, 8},
	} {
		t.Run(c.op+"/"+c.err.(*smithy.GenericAPIError).Code, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, auibOpLabel(c.op))
			calls := newAUIBCalls()
			h := auibHandlers(calls)
			h[c.op] = func(any) (any, error) { return nil, c.err }

			total, err := auibScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (%s: %d); want 1 under the op", *warns, auibOpLabel(c.op), *opWarns)
			}
			if stored := countStoredRows(t, st); total != c.want || stored != c.want {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, c.want)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanAmplifyUIBuilder_FailedWriteStopsWithoutWarning(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeAmplifyUIBuilderComponent)
	calls := newAUIBCalls()
	calls.errs[auibComponentARN("dt", "t")] = crDenied

	_, err := auibScan(context.Background(), t, st, auibHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListForms", "ListThemes"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A listing's failed page keeps the pages read before it, one environment's
// failure costs the other none of its rows, and no listing's failure stops
// the phases after it. The entity listings model no not-found code, so
// ResourceNotFoundException is returned like any other code.
func TestScanAmplifyUIBuilder_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newAUIBCalls()
	h := auibHandlers(calls)
	components := h["ListComponents"]
	h["ListComponents"] = func(in any) (any, error) {
		if app, _ := auibEnvKey(in); app == "dt" && bacField[*string](in, "NextToken") != nil {
			return nil, auibBadInput
		}
		return components(in)
	}
	forms := h["ListForms"]
	h["ListForms"] = func(in any) (any, error) {
		if app, _ := auibEnvKey(in); app == "db" {
			return nil, crNotFound
		}
		return forms(in)
	}

	total, err := auibScan(context.Background(), t, st, h)
	for _, code := range []string{"InvalidParameterException", "ResourceNotFoundException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	if total != 9 {
		t.Errorf("total = %d; want 9 (12 less dt's c-b and db's two forms)", total)
	}
	for _, want := range []struct{ typ, arn string }{
		{TypeAmplifyUIBuilderComponent, auibComponentARN("dt", "t")},
		{TypeAmplifyUIBuilderComponent, auibComponentARN("db", "b")},
		{TypeAmplifyUIBuilderForm, auibTestARN("aws", testRegion, "dt", "prod", "forms", "f-b")},
		{TypeAmplifyUIBuilderTheme, auibTestARN("aws", testRegion, "db", "dev", "themes", "th-b")},
	} {
		if _, ok := auibRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
	for _, gone := range []struct{ typ, arn string }{
		{TypeAmplifyUIBuilderComponent, auibComponentARN("dt", "b")},
		{TypeAmplifyUIBuilderForm, auibTestARN("aws", testRegion, "db", "dev", "forms", "f-t")},
	} {
		if _, ok := auibRowOf(t, st, gone.typ, gone.arn); ok {
			t.Errorf("%s %s stored; want it not read", gone.typ, gone.arn)
		}
	}
}

// An app listing that fails after its first page keeps the apps of that page,
// whose resources are stored, and its error is filed under auibAppsOp:
// throttled beside a hard error of another listing, it is demoted to one
// warning under that op. An environment listing failing for one app costs
// the other app none of its resources, and its error is returned: the op
// models no not-found code. BadRequestException on an app's first
// environment page, the op refusing an app it does not serve (Gen 2), reads
// as an app with no environments; after a page it served, it is returned.
func TestScanAmplifyUIBuilder_ParentListFailures(t *testing.T) {
	t.Parallel()
	t.Run("app page 2 throttled", func(t *testing.T) {
		st := newTestStore(t)
		opWarns := countOpWarnings(st, auibAppsOp)
		h := auibHandlers(newAUIBCalls())
		h["ListApps"] = failOnCall(2, crThrottled, h["ListApps"])
		h["ListThemes"] = func(any) (any, error) { return nil, auibBadInput }

		total, err := auibScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "InvalidParameterException")
		if *opWarns != 1 {
			t.Errorf("%s warnings = %d; want 1, the demoted throttle", auibAppsOp, *opWarns)
		}
		if total != 4 {
			t.Errorf("total = %d; want 4, dt's components and forms", total)
		}
		if _, ok := auibRowOf(t, st, TypeAmplifyUIBuilderComponent, auibComponentARN("db", "t")); ok {
			t.Error("component of db stored; want its app not read")
		}
	})
	t.Run("one app's environments fail", func(t *testing.T) {
		for _, c := range []struct {
			name    string
			err     error
			outcome serviceOutcome
		}{
			{"not found", apiErr("NotFoundException", "gone"), outcomeError},
			{"bad request", apiErr("BadRequestException", "not a Gen 1 app"), 0},
			{"throttled", crThrottled, outcomeTransient},
		} {
			t.Run(c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				h := auibHandlers(newAUIBCalls())
				envs := h["ListBackendEnvironments"]
				h["ListBackendEnvironments"] = func(in any) (any, error) {
					if sv(in.(*amplify.ListBackendEnvironmentsInput).AppId) == "dt" {
						return nil, c.err
					}
					return envs(in)
				}

				total, err := auibScan(context.Background(), t, st, h)
				if c.outcome == 0 && err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if c.outcome != 0 {
					assertGlueOutcome(t, err, c.outcome)
				}
				if total != 6 || *warns != 0 {
					t.Errorf("total, warnings = %d, %d; want 6, 0", total, *warns)
				}
				if _, ok := auibRowOf(t, st, TypeAmplifyUIBuilderComponent, auibComponentARN("db", "t")); !ok {
					t.Error("component of db missing")
				}
			})
		}
	})
	t.Run("bad request after a served page", func(t *testing.T) {
		st := newTestStore(t)
		h := auibHandlers(newAUIBCalls())
		envs := h["ListBackendEnvironments"]
		h["ListBackendEnvironments"] = func(in any) (any, error) {
			li := in.(*amplify.ListBackendEnvironmentsInput)
			if sv(li.AppId) != "dt" {
				return envs(in)
			}
			if li.NextToken != nil {
				return nil, apiErr("BadRequestException", "bad token")
			}
			return &amplify.ListBackendEnvironmentsOutput{BackendEnvironments: []amplifytypes.BackendEnvironment{{EnvironmentName: sdkaws.String("prod")}}, NextToken: sdkaws.String("p2")}, nil
		}

		total, err := auibScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "BadRequestException")
		if stored := countStoredRows(t, st); total != 12 || stored != 12 {
			t.Errorf("total = %d, stored rows = %d; want 12, dt's page 1 environment kept", total, stored)
		}
	})
}

// Entities listed without an Id, apps without an AppId and environments
// without an EnvironmentName are not stored or read, and are warned about
// once per listing, under a key that is never empty.
func TestScanAmplifyUIBuilder_UnstorableItemsWarn(t *testing.T) {
	t.Parallel()
	appARN := partitionARN("aws", "amplify", testRegion, "apps/x")
	envARN := partitionARN("aws", "amplify", testRegion, "apps/dt/backendenvironments/staging")
	for _, c := range []struct {
		name, op, key string
		out           any
		want          int
	}{
		{"unnamed component", "ListComponents", "first (an element with no identifier) in dt/prod", auibPage("ListComponents", nil, auibtypes.ComponentSummary{}, auibtypes.ComponentSummary{}), 10},
		{"named theme", "ListThemes", "first named theme in db/dev", auibPage("ListThemes", nil, auibtypes.ThemeSummary{Name: sdkaws.String("named theme")}), 10},
		{"unnamed app", "ListApps", "first (an element with no identifier)", &amplify.ListAppsOutput{Apps: []amplifytypes.App{{}}}, 0},
		{"named app", "ListApps", "first named app", &amplify.ListAppsOutput{Apps: []amplifytypes.App{{Name: sdkaws.String("named app"), AppArn: &appARN}}}, 0},
		{"app with only an ARN", "ListApps", "first " + appARN, &amplify.ListAppsOutput{Apps: []amplifytypes.App{{AppArn: &appARN}}}, 0},
		{"unnamed environment", "ListBackendEnvironments", "first (an element with no identifier)", &amplify.ListBackendEnvironmentsOutput{BackendEnvironments: []amplifytypes.BackendEnvironment{{}}}, 6},
		{"environment with an ARN", "ListBackendEnvironments", "first " + envARN, &amplify.ListBackendEnvironmentsOutput{BackendEnvironments: []amplifytypes.BackendEnvironment{{BackendEnvironmentArn: &envARN}}}, 6},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, auibOpLabel(c.op))
			calls := newAUIBCalls()
			h := auibHandlers(calls)
			next := h[c.op]
			h[c.op] = func(in any) (any, error) {
				// Only one app's or environment's listing returns the
				// element, so the key names it.
				if app, _ := auibEnvKey(in); c.op == "ListApps" || (c.op == "ListComponents" && app == "dt") || (c.op == "ListThemes" && app == "db") || (c.op == "ListBackendEnvironments" && app == "dt") {
					return c.out, nil
				}
				return next(in)
			}

			total, err := auibScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %v; want one naming %q", *warnings, c.key)
			}
			if total != c.want {
				t.Errorf("total = %d; want %d", total, c.want)
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages. An app
// or environment the repeated page hands over again is listed once: its
// environments are listed once, and its resources are stored, tag-read and
// counted once.
func TestScanAmplifyUIBuilder_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op       string
		page     func(in any) (any, error)
		want     int
		envCalls int32
	}{
		// Only dt, so only its 6 resources.
		{"ListApps", func(any) (any, error) {
			return &amplify.ListAppsOutput{Apps: []amplifytypes.App{auibApp("dt")}, NextToken: sdkaws.String("same")}, nil
		}, 6, 1},
		// Both apps, each with its one environment.
		{"ListBackendEnvironments", func(in any) (any, error) {
			app := sv(in.(*amplify.ListBackendEnvironmentsInput).AppId)
			return &amplify.ListBackendEnvironmentsOutput{BackendEnvironments: []amplifytypes.BackendEnvironment{{EnvironmentName: sdkaws.String(auibEnvOf[app])}}, NextToken: sdkaws.String("same")}, nil
		}, 12, 4},
		// The token repeats on page 2, after c-b was read.
		{"ListComponents", func(in any) (any, error) {
			app, env := auibEnvKey(in)
			id := "c-t"
			if bacField[*string](in, "NextToken") != nil {
				id = "c-b"
			}
			return auibPage("ListComponents", sdkaws.String("same"), auibElement(auibKinds[0], app, env, id)), nil
		}, 12, 2},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newAUIBCalls()
			h := auibHandlers(calls)
			h[c.op] = c.page
			var envCalls atomic.Int32
			envs := h["ListBackendEnvironments"]
			h["ListBackendEnvironments"] = func(in any) (any, error) {
				envCalls.Add(1)
				return envs(in)
			}

			total, err := auibScan(context.Background(), t, st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := auibRowOf(t, st, TypeAmplifyUIBuilderComponent, auibComponentARN("dt", "t")); !ok {
				t.Error("component c-t of dt, from the pages read, missing")
			}
			if stored := countStoredRows(t, st); total != c.want || stored != c.want || len(calls.tagARNs) != c.want {
				t.Errorf("total, stored rows, tag reads = %d, %d, %d; want %d each", total, stored, len(calls.tagARNs), c.want)
			}
			if n := envCalls.Load(); n != c.envCalls {
				t.Errorf("ListBackendEnvironments calls = %d; want %d", n, c.envCalls)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is reported once.
func TestScanAmplifyUIBuilder_ContextEndStopsOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newAUIBCalls()
	h := auibHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forms := h["ListForms"]
	h["ListForms"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return forms(in)
	}

	_, err := auibScan(ctx, t, st, h)
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
	if n := calls.count("ListThemes"); n != 0 {
		t.Errorf("ListThemes called %d times after ctx ended", n)
	}
}

// A region without UI Builder (NXDOMAIN on the first entity listing, nothing
// stored) is unavailable, and no later listing is made. A region without
// Amplify (NXDOMAIN on the first app page) has no apps: nothing is listed
// and it is not an error. An NXDOMAIN once an endpoint resolved is a failure
// of this scan, not a region gap: the counts of the rows stored are kept.
func TestScanAmplifyUIBuilder_Unreachable(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "amplifyuibuilder.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("UI Builder endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		calls := newAUIBCalls()
		h := auibHandlers(calls)
		h["ListComponents"] = func(any) (any, error) { return nil, nx }
		total, err := auibScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("ListForms") != 0 || calls.count("ListThemes") != 0 {
			t.Errorf("total = %d, ListForms, ListThemes calls = %d, %d; want 0, 0, 0", total, calls.count("ListForms"), calls.count("ListThemes"))
		}
	})
	t.Run("Amplify endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls := newAUIBCalls()
		h := auibHandlers(calls)
		h["ListApps"] = func(any) (any, error) { return nil, nx }
		total, err := auibScan(context.Background(), t, st, h)
		if err != nil || total != 0 || *warns != 0 {
			t.Errorf("total, warnings, err = %d, %d, %v; want 0, 0, nil", total, *warns, err)
		}
		if n := calls.count("ListComponents") + calls.count("ListBackendEnvironments"); n != 0 {
			t.Errorf("ListBackendEnvironments + ListComponents calls = %d; want 0", n)
		}
	})
	for _, c := range []struct {
		name, op string
		want     int
	}{
		{"Amplify endpoint gone after app page 1", "ListApps", 6},
		{"Amplify endpoint gone at the environments", "ListBackendEnvironments", 6},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := auibHandlers(newAUIBCalls())
			h[c.op] = failOnCall(2, nx, h[c.op])
			total, err := auibScan(context.Background(), t, st, h)
			assertGlueOutcome(t, err, outcomeError)
			if isDNSNotFound(err) || total != c.want {
				t.Errorf("total = %d, err = %v; want %d, the NXDOMAIN flattened", total, err, c.want)
			}
		})
	}
	t.Run("Amplify endpoint gone after an empty app page", func(t *testing.T) {
		st := newTestStore(t)
		calls := newAUIBCalls()
		h := auibHandlers(calls)
		h["ListApps"] = func(in any) (any, error) {
			if in.(*amplify.ListAppsInput).NextToken == nil {
				return &amplify.ListAppsOutput{NextToken: sdkaws.String("p2")}, nil
			}
			return nil, nx
		}
		total, err := auibScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeError)
		if isDNSNotFound(err) || total != 0 || calls.count("ListBackendEnvironments") != 0 {
			t.Errorf("total, ListBackendEnvironments calls, err = %d, %d, %v; want 0, 0, the NXDOMAIN flattened", total, calls.count("ListBackendEnvironments"), err)
		}
	})
	// With nothing stored, resultLateNXDOMAIN would leave the NXDOMAIN as
	// is: only the Amplify listings' own flattening keeps the region from
	// reading as unavailable.
	for _, op := range []string{"ListApps", "ListBackendEnvironments"} {
		t.Run("Amplify endpoint gone at "+op+" with nothing stored", func(t *testing.T) {
			st := newTestStore(t)
			h := auibHandlers(newAUIBCalls())
			h[op] = failOnCall(2, nx, h[op])
			for _, k := range auibKinds {
				h[k.op] = func(any) (any, error) { return auibPage(k.op, nil), nil }
			}
			_, err := auibScan(context.Background(), t, st, h)
			assertGlueOutcome(t, err, outcomeError)
		})
	}
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls := newAUIBCalls()
		calls.errs[auibComponentARN("dt", "t")] = nx
		total, err := auibScan(context.Background(), t, st, auibHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != 12 {
			t.Errorf("total = %d; want 12", total)
		}
	})
}
