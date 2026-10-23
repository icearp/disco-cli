package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	cbldDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: codebuild:List"}
	cbldBadInput  = &smithy.GenericAPIError{Code: "InvalidInputException", Message: "bad"}
	cbldThrottled = apiErr("ThrottlingException", "Rate exceeded")
	cbldTags      = map[string]string{"team": "build"}
)

func cbldARN(kind, name string) string {
	return fmt.Sprintf("arn:aws:codebuild:%s:%s:%s/%s", testRegion, testAccountID, kind, name)
}

func cbldTagList() []cbtypes.Tag {
	return []cbtypes.Tag{{Key: ptrStr("team"), Value: ptrStr("build")}}
}

// cbldFixture is what the stub serves. Each listing is two pages of keys; a
// BatchGet op returns the element mapped to each key it is sent and reports
// the keys with none as not found.
type cbldFixture struct {
	fleetPages, projectPages, groupPages [][]string
	fleets                               map[string]cbtypes.Fleet
	projects                             map[string]cbtypes.Project
	groups                               map[string]cbtypes.ReportGroup
	creds                                []cbtypes.SourceCredentialsInfo
}

// newCBFixture lists, per type, a tagged element and one with no ARN on page
// one, and an untagged element and one BatchGet does not find on page two.
// The untagged fleet and report group have no name.
func newCBFixture() cbldFixture {
	return cbldFixture{
		fleetPages: [][]string{{cbldARN("fleet", "f1"), "f-noarn"}, {cbldARN("fleet", "f2"), cbldARN("fleet", "f-gone")}},
		fleets: map[string]cbtypes.Fleet{
			cbldARN("fleet", "f1"): {Arn: ptrStr(cbldARN("fleet", "f1")), Name: ptrStr("f1"), FleetServiceRole: ptrStr("arn:aws:iam::123456789012:role/r"), Tags: cbldTagList()},
			"f-noarn":              {Name: ptrStr("f-noarn")},
			cbldARN("fleet", "f2"): {Arn: ptrStr(cbldARN("fleet", "f2"))},
		},
		projectPages: [][]string{{"p1", "p-noarn"}, {"p2", "p-gone"}},
		projects: map[string]cbtypes.Project{
			"p1":      {Arn: ptrStr(cbldARN("project", "p1")), Name: ptrStr("p1"), ServiceRole: ptrStr("arn:aws:iam::123456789012:role/r"), Tags: cbldTagList()},
			"p-noarn": {Name: ptrStr("p-noarn")},
			"p2":      {Arn: ptrStr(cbldARN("project", "p2")), Name: ptrStr("p2")},
		},
		groupPages: [][]string{{cbldARN("report-group", "g1"), "g-noarn"}, {cbldARN("report-group", "g2"), cbldARN("report-group", "g-gone")}},
		groups: map[string]cbtypes.ReportGroup{
			cbldARN("report-group", "g1"): {Arn: ptrStr(cbldARN("report-group", "g1")), Name: ptrStr("g1"), Type: cbtypes.ReportTypeTest, Tags: cbldTagList()},
			"g-noarn":                     {Name: ptrStr("g-noarn")},
			cbldARN("report-group", "g2"): {Arn: ptrStr(cbldARN("report-group", "g2"))},
		},
		creds: []cbtypes.SourceCredentialsInfo{
			{Arn: ptrStr(cbldARN("token", "github")), ServerType: cbtypes.ServerTypeGithub, AuthType: cbtypes.AuthTypeOauth},
			{ServerType: cbtypes.ServerTypeBitbucket},
		},
	}
}

// cbldBulkFixture lists 101 keys per type on one page, each with an element,
// so each BatchGet op is called with a full batch and a batch of one.
func cbldBulkFixture() cbldFixture {
	f := cbldFixture{fleets: map[string]cbtypes.Fleet{}, projects: map[string]cbtypes.Project{}, groups: map[string]cbtypes.ReportGroup{}}
	var fleets, projects, groups []string
	for i := range 101 {
		n := fmt.Sprintf("k%03d", i)
		fleets = append(fleets, cbldARN("fleet", n))
		f.fleets[cbldARN("fleet", n)] = cbtypes.Fleet{Arn: ptrStr(cbldARN("fleet", n)), Name: ptrStr(n)}
		projects = append(projects, n)
		f.projects[n] = cbtypes.Project{Arn: ptrStr(cbldARN("project", n)), Name: ptrStr(n)}
		groups = append(groups, cbldARN("report-group", n))
		f.groups[cbldARN("report-group", n)] = cbtypes.ReportGroup{Arn: ptrStr(cbldARN("report-group", n)), Name: ptrStr(n)}
	}
	f.fleetPages = [][]string{fleets, {}}
	f.projectPages = [][]string{projects, {}}
	f.groupPages = [][]string{groups, {}}
	return f
}

// cbldServePage returns page token ("" or "p2") of pages, failing a call whose
// page size is not want (nil: the op takes none), whose sort is set, or whose
// token names no page, and returning fail[token] when set.
func cbldServePage(pages [][]string, token *string, maxResults, want *int32, sortBy, sortOrder string, fail map[string]error) ([]string, *string, error) {
	if (want == nil) != (maxResults == nil) || (want != nil && *maxResults != *want) {
		return nil, nil, fmt.Errorf("MaxResults = %v, want %v", maxResults, want)
	}
	if sortBy != "" || sortOrder != "" {
		return nil, nil, fmt.Errorf("sort = %q/%q, want unset", sortBy, sortOrder)
	}
	if err := fail[sv(token)]; err != nil {
		return nil, nil, err
	}
	switch {
	case token == nil:
		return pages[0], ptrStr("p2"), nil
	case *token == "p2":
		return pages[1], nil, nil
	}
	return nil, nil, fmt.Errorf("unexpected NextToken %q", *token)
}

// cbldBatches records the sizes of the batches each BatchGet op was sent.
type cbldBatches struct {
	mu    sync.Mutex
	sizes map[string][]int
}

// cbldServeBatch returns the elements of keys and the keys with none, failing
// a batch outside the op's 1-100 bound and returning fail[first key] when set.
func cbldServeBatch[Elem any](b *cbldBatches, op string, keys []string, elems map[string]Elem, fail map[string]error) ([]Elem, []string, error) {
	b.mu.Lock()
	b.sizes[op] = append(b.sizes[op], len(keys))
	b.mu.Unlock()
	if len(keys) < 1 || len(keys) > 100 {
		return nil, nil, fmt.Errorf("%s sent %d keys, want 1-100", op, len(keys))
	}
	if err := fail[keys[0]]; err != nil {
		return nil, nil, err
	}
	var (
		found    []Elem
		notFound []string
	)
	for _, k := range keys {
		if e, ok := elems[k]; ok {
			found = append(found, e)
		} else {
			notFound = append(notFound, k)
		}
	}
	return found, notFound, nil
}

var cbldPageMaxWant = ptrInt32(100)

// cbldHandlers serves f; fail is keyed by op, then by the token sent ("" on
// page one) or a batch's first key. calls records the op of each call.
func cbldHandlers(f cbldFixture, fail map[string]map[string]error, calls *parentCalls, batches *cbldBatches) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListFleets": func(in any) (any, error) {
			calls.add("ListFleets")
			p := in.(*codebuild.ListFleetsInput)
			keys, next, err := cbldServePage(f.fleetPages, p.NextToken, p.MaxResults, cbldPageMaxWant, string(p.SortBy), string(p.SortOrder), fail["ListFleets"])
			if err != nil {
				return nil, err
			}
			return &codebuild.ListFleetsOutput{Fleets: keys, NextToken: next}, nil
		},
		"BatchGetFleets": func(in any) (any, error) {
			calls.add("BatchGetFleets")
			found, notFound, err := cbldServeBatch(batches, "BatchGetFleets", in.(*codebuild.BatchGetFleetsInput).Names, f.fleets, fail["BatchGetFleets"])
			if err != nil {
				return nil, err
			}
			return &codebuild.BatchGetFleetsOutput{Fleets: found, FleetsNotFound: notFound}, nil
		},
		"ListProjects": func(in any) (any, error) {
			calls.add("ListProjects")
			p := in.(*codebuild.ListProjectsInput)
			keys, next, err := cbldServePage(f.projectPages, p.NextToken, nil, nil, string(p.SortBy), string(p.SortOrder), fail["ListProjects"])
			if err != nil {
				return nil, err
			}
			return &codebuild.ListProjectsOutput{Projects: keys, NextToken: next}, nil
		},
		"BatchGetProjects": func(in any) (any, error) {
			calls.add("BatchGetProjects")
			found, notFound, err := cbldServeBatch(batches, "BatchGetProjects", in.(*codebuild.BatchGetProjectsInput).Names, f.projects, fail["BatchGetProjects"])
			if err != nil {
				return nil, err
			}
			return &codebuild.BatchGetProjectsOutput{Projects: found, ProjectsNotFound: notFound}, nil
		},
		"ListReportGroups": func(in any) (any, error) {
			calls.add("ListReportGroups")
			p := in.(*codebuild.ListReportGroupsInput)
			keys, next, err := cbldServePage(f.groupPages, p.NextToken, p.MaxResults, cbldPageMaxWant, string(p.SortBy), string(p.SortOrder), fail["ListReportGroups"])
			if err != nil {
				return nil, err
			}
			return &codebuild.ListReportGroupsOutput{ReportGroups: keys, NextToken: next}, nil
		},
		"BatchGetReportGroups": func(in any) (any, error) {
			calls.add("BatchGetReportGroups")
			found, notFound, err := cbldServeBatch(batches, "BatchGetReportGroups", in.(*codebuild.BatchGetReportGroupsInput).ReportGroupArns, f.groups, fail["BatchGetReportGroups"])
			if err != nil {
				return nil, err
			}
			return &codebuild.BatchGetReportGroupsOutput{ReportGroups: found, ReportGroupsNotFound: notFound}, nil
		},
		"ListSourceCredentials": func(any) (any, error) {
			calls.add("ListSourceCredentials")
			if err := fail["ListSourceCredentials"][""]; err != nil {
				return nil, err
			}
			return &codebuild.ListSourceCredentialsOutput{SourceCredentialsInfos: f.creds}, nil
		},
	}
}

var cbldOpLabels = map[string]string{
	"ListFleets":            "codebuild:ListFleets",
	"BatchGetFleets":        "codebuild:BatchGetFleets",
	"ListProjects":          "codebuild:ListProjects",
	"BatchGetProjects":      "codebuild:BatchGetProjects",
	"ListReportGroups":      "codebuild:ListReportGroups",
	"BatchGetReportGroups":  "codebuild:BatchGetReportGroups",
	"ListSourceCredentials": "codebuild:ListSourceCredentials",
}

// cbldResult is what one top-level scan returned and recorded.
type cbldResult struct {
	total, inserted int
	err             error
	warns           map[string]*int
	warnings        []store.ScanWarning
	calls           *parentCalls
	batches         *cbldBatches
}

func scanCBTopLevel(t *testing.T, st *store.Store, f cbldFixture, fail map[string]map[string]error) cbldResult {
	t.Helper()
	res := cbldResult{warns: map[string]*int{}, calls: &parentCalls{}, batches: &cbldBatches{sizes: map[string][]int{}}}
	for op, label := range cbldOpLabels {
		res.warns[op] = countOpWarnings(st, label)
	}
	prev := st.OnWarn
	st.OnWarn = func(w store.ScanWarning) {
		res.warnings = append(res.warnings, w)
		prev(w)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(cbldHandlers(f, fail, res.calls, res.batches), cbldDenied)
	res.total, res.inserted, res.err = scanCodeBuild(context.Background(), acct, testRegion, st, testScanID)
	return res
}

// cbldAllRows is every NativeID newCBFixture stores, per type.
var cbldAllRows = map[string][]string{
	TypeCodeBuildFleet:            {cbldARN("fleet", "f1"), cbldARN("fleet", "f2")},
	TypeCodeBuildProject:          {cbldARN("project", "p1"), cbldARN("project", "p2")},
	TypeCodeBuildReportGroup:      {cbldARN("report-group", "g1"), cbldARN("report-group", "g2")},
	TypeCodeBuildSourceCredential: {cbldARN("token", "github")},
}

var cbldTypes = []string{TypeCodeBuildFleet, TypeCodeBuildProject, TypeCodeBuildReportGroup, TypeCodeBuildSourceCredential}

func assertCBRows(t *testing.T, st *store.Store, want map[string][]string) {
	t.Helper()
	for _, typ := range cbldTypes {
		got := nativeIDs(rowsByNativeID(t, st, typ))
		if !slices.Equal(got, want[typ]) {
			t.Errorf("%s rows = %v, want %v", typ, got, want[typ])
		}
	}
}

// cbldRowsWithout is cbldAllRows with typ's rows replaced by keep.
func cbldRowsWithout(typ string, keep ...string) map[string][]string {
	want := map[string][]string{}
	for k, v := range cbldAllRows {
		want[k] = v
	}
	want[typ] = keep
	return want
}

// assertCBRow checks a stored row's name, region, attributes and tags (nil
// wantTags: no tags stored).
func assertCBRow(t *testing.T, row store.Resource, name string, attrs any, wantTags map[string]string) {
	t.Helper()
	if sv(row.Name) != name || sv(row.Region) != testRegion || row.AttributesJSON != mustJSON(attrs) {
		t.Errorf("%s: name/region = %q/%q, attrs = %s; want %q/%s, %s", row.NativeID, sv(row.Name), sv(row.Region), row.AttributesJSON, name, testRegion, mustJSON(attrs))
	}
	if wantTags == nil {
		if row.TagsJSON != nil {
			t.Errorf("%s: tags = %s, want none", row.NativeID, *row.TagsJSON)
		}
		return
	}
	assertTagsMap(t, row.TagsJSON, wantTags)
}

// hasCBWarning reports whether a warning under op names key: a ScanWarning
// has no key field, so the key is matched in the message.
func hasCBWarning(warnings []store.ScanWarning, op, key string) bool {
	return slices.ContainsFunc(warnings, func(w store.ScanWarning) bool {
		return w.Service == cbldOpLabels[op] && strings.Contains(w.Message, key)
	})
}

func TestScanCodeBuild_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	res := scanCBTopLevel(t, st, newCBFixture(), nil)
	if res.err != nil {
		t.Fatalf("scanCodeBuild: %v", res.err)
	}
	if res.total != 7 || res.inserted != 7 {
		t.Errorf("total/inserted = %d/%d, want 7/7", res.total, res.inserted)
	}
	assertCBRows(t, st, cbldAllRows)

	f := newCBFixture()
	fleets := rowsByNativeID(t, st, TypeCodeBuildFleet)
	assertCBRow(t, fleets[cbldARN("fleet", "f1")], "f1", f.fleets[cbldARN("fleet", "f1")], cbldTags)
	// A fleet with no name is named by its ARN.
	assertCBRow(t, fleets[cbldARN("fleet", "f2")], cbldARN("fleet", "f2"), f.fleets[cbldARN("fleet", "f2")], nil)
	projects := rowsByNativeID(t, st, TypeCodeBuildProject)
	assertCBRow(t, projects[cbldARN("project", "p1")], "p1", f.projects["p1"], cbldTags)
	assertCBRow(t, projects[cbldARN("project", "p2")], "p2", f.projects["p2"], nil)
	groups := rowsByNativeID(t, st, TypeCodeBuildReportGroup)
	assertCBRow(t, groups[cbldARN("report-group", "g1")], "g1", f.groups[cbldARN("report-group", "g1")], cbldTags)
	assertCBRow(t, groups[cbldARN("report-group", "g2")], cbldARN("report-group", "g2"), f.groups[cbldARN("report-group", "g2")], nil)
	creds := rowsByNativeID(t, st, TypeCodeBuildSourceCredential)
	assertCBRow(t, creds[cbldARN("token", "github")], "GITHUB", f.creds[0], nil)

	// Per BatchGet op one warning for the element with no ARN and one for
	// the key not found; one for the source credential with no ARN.
	wantWarns := map[string][]string{
		"BatchGetFleets":        {"f-noarn", cbldARN("fleet", "f-gone")},
		"BatchGetProjects":      {"p-noarn", "p-gone"},
		"BatchGetReportGroups":  {"g-noarn", cbldARN("report-group", "g-gone")},
		"ListSourceCredentials": {"BITBUCKET"},
	}
	for op, label := range cbldOpLabels {
		if got, want := *res.warns[op], len(wantWarns[op]); got != want {
			t.Errorf("%s warnings = %d, want %d", label, got, want)
		}
	}
	for op, keys := range wantWarns {
		for _, key := range keys {
			if !hasCBWarning(res.warnings, op, key) {
				t.Errorf("%s: no warning names %q; warnings = %v", op, key, res.warnings)
			}
		}
	}
	for _, w := range res.warnings {
		if w.Scope != testAccountID+"/"+testRegion {
			t.Errorf("warning scope = %q, want %s/%s", w.Scope, testAccountID, testRegion)
		}
	}
}

// Each BatchGet op is sent its keys 100 at a time, the model's cap, and
// every batch's elements are stored.
func TestScanCodeBuild_BatchesAtModelCap(t *testing.T) {
	st := newTestStore(t)
	res := scanCBTopLevel(t, st, cbldBulkFixture(), nil)
	if res.err != nil {
		t.Fatalf("scanCodeBuild: %v", res.err)
	}
	for _, op := range []string{"BatchGetFleets", "BatchGetProjects", "BatchGetReportGroups"} {
		got := slices.Sorted(slices.Values(res.batches.sizes[op]))
		if !slices.Equal(got, []int{1, 100}) {
			t.Errorf("%s batch sizes = %v, want [1 100]", op, got)
		}
	}
	for _, typ := range []string{TypeCodeBuildFleet, TypeCodeBuildProject, TypeCodeBuildReportGroup} {
		if n := len(rowsByNativeID(t, st, typ)); n != 101 {
			t.Errorf("%s rows = %d, want 101", typ, n)
		}
	}
	if res.total != 303 || res.inserted != 303 {
		t.Errorf("total/inserted = %d/%d, want 303/303", res.total, res.inserted)
	}
}

// A failed batch keeps the other batch's rows and is returned; a throttled
// one is returned too, not warned about, since its listed keys cannot be
// stored without it; every later phase still runs.
func TestScanCodeBuild_FailedBatchKeepsOtherBatches(t *testing.T) {
	for _, tc := range []struct {
		op, typ, first, kept string
		err                  error
		want                 serviceOutcome
	}{
		{"BatchGetFleets", TypeCodeBuildFleet, cbldARN("fleet", "k000"), cbldARN("fleet", "k100"), cbldBadInput, outcomeError},
		{"BatchGetProjects", TypeCodeBuildProject, "k000", cbldARN("project", "k100"), cbldBadInput, outcomeError},
		{"BatchGetReportGroups", TypeCodeBuildReportGroup, cbldARN("report-group", "k000"), cbldARN("report-group", "k100"), cbldBadInput, outcomeError},
		{"BatchGetFleets", TypeCodeBuildFleet, cbldARN("fleet", "k000"), cbldARN("fleet", "k100"), cbldThrottled, outcomeTransient},
		{"BatchGetProjects", TypeCodeBuildProject, "k000", cbldARN("project", "k100"), cbldThrottled, outcomeTransient},
		{"BatchGetReportGroups", TypeCodeBuildReportGroup, cbldARN("report-group", "k000"), cbldARN("report-group", "k100"), cbldThrottled, outcomeTransient},
	} {
		t.Run(tc.op+"/"+tc.err.(smithy.APIError).ErrorCode(), func(t *testing.T) {
			st := newTestStore(t)
			res := scanCBTopLevel(t, st, cbldBulkFixture(), map[string]map[string]error{tc.op: {tc.first: tc.err}})
			if got := classifyServiceError(res.err); got != tc.want {
				t.Fatalf("classifyServiceError(%v) = %v, want %v", res.err, got, tc.want)
			}
			var ae smithy.APIError
			if !errors.As(res.err, &ae) || ae.ErrorCode() != tc.err.(smithy.APIError).ErrorCode() {
				t.Errorf("err = %v, want the %s batch error", res.err, tc.op)
			}
			if got := nativeIDs(rowsByNativeID(t, st, tc.typ)); !slices.Equal(got, []string{tc.kept}) {
				t.Errorf("%s rows = %v, want [%s]", tc.typ, got, tc.kept)
			}
			if *res.warns[tc.op] != 0 {
				t.Errorf("%s warnings = %d, want 0", tc.op, *res.warns[tc.op])
			}
			if res.total != 1+202 || res.inserted != 1+202 {
				t.Errorf("total/inserted = %d/%d, want 203/203", res.total, res.inserted)
			}
			if !slices.Contains(res.calls.distinct(), "ListSourceCredentials") {
				t.Errorf("ops called = %v; the source-credential phase did not run", res.calls.distinct())
			}
		})
	}
}

// Denied batches warn once per op, naming the first batch's first key, and
// are not an error; the other phases keep their rows.
func TestScanCodeBuild_DeniedBatchesWarnOnce(t *testing.T) {
	for _, tc := range []struct{ op, typ, first, second string }{
		{"BatchGetFleets", TypeCodeBuildFleet, cbldARN("fleet", "k000"), cbldARN("fleet", "k100")},
		{"BatchGetProjects", TypeCodeBuildProject, "k000", "k100"},
		{"BatchGetReportGroups", TypeCodeBuildReportGroup, cbldARN("report-group", "k000"), cbldARN("report-group", "k100")},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			res := scanCBTopLevel(t, st, cbldBulkFixture(), map[string]map[string]error{tc.op: {tc.first: cbldDenied, tc.second: cbldDenied}})
			if res.err != nil {
				t.Fatalf("err = %v, want nil", res.err)
			}
			if *res.warns[tc.op] != 1 || !hasCBWarning(res.warnings, tc.op, tc.first) {
				t.Errorf("%s warnings = %d, want 1 naming %s; warnings = %v", tc.op, *res.warns[tc.op], tc.first, res.warnings)
			}
			if n := len(rowsByNativeID(t, st, tc.typ)); n != 0 {
				t.Errorf("%s rows = %d, want 0", tc.typ, n)
			}
			if res.total != 202 || res.inserted != 202 {
				t.Errorf("total/inserted = %d/%d, want 202/202", res.total, res.inserted)
			}
		})
	}
}

// A failed page two keeps the keys read before it, whose elements are read
// and stored, and the error is returned; every other phase still runs.
func TestScanCodeBuild_FailedPageKeepsRowsAndRunsOtherPhases(t *testing.T) {
	for op, tc := range map[string]struct {
		typ  string
		keep []string
	}{
		"ListFleets":       {TypeCodeBuildFleet, []string{cbldARN("fleet", "f1")}},
		"ListProjects":     {TypeCodeBuildProject, []string{cbldARN("project", "p1")}},
		"ListReportGroups": {TypeCodeBuildReportGroup, []string{cbldARN("report-group", "g1")}},
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			res := scanCBTopLevel(t, st, newCBFixture(), map[string]map[string]error{op: {"p2": cbldBadInput}})
			if classifyServiceError(res.err) != outcomeError || !isAPIErrorCode(res.err, "InvalidInputException") {
				t.Fatalf("err = %v, want an InvalidInputException error", res.err)
			}
			if res.total != 6 || res.inserted != 6 {
				t.Errorf("total/inserted = %d/%d, want 6/6", res.total, res.inserted)
			}
			assertCBRows(t, st, cbldRowsWithout(tc.typ, tc.keep...))
		})
	}
}

// A denied listing warns once for its op and is not an error; the other
// phases keep their rows.
func TestScanCodeBuild_DeniedListingWarnsOnce(t *testing.T) {
	for op, typ := range map[string]string{
		"ListFleets":            TypeCodeBuildFleet,
		"ListProjects":          TypeCodeBuildProject,
		"ListReportGroups":      TypeCodeBuildReportGroup,
		"ListSourceCredentials": TypeCodeBuildSourceCredential,
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			res := scanCBTopLevel(t, st, newCBFixture(), map[string]map[string]error{op: {"": cbldDenied}})
			if res.err != nil {
				t.Fatalf("err = %v, want nil", res.err)
			}
			if *res.warns[op] != 1 {
				t.Errorf("%s warnings = %d, want 1", op, *res.warns[op])
			}
			want := 7 - len(cbldAllRows[typ])
			if res.total != want || res.inserted != want {
				t.Errorf("total/inserted = %d/%d, want %d/%d", res.total, res.inserted, want, want)
			}
			assertCBRows(t, st, cbldRowsWithout(typ))
		})
	}
}

// ListFleets answering "Unknown operation" on its first page is a region
// without fleets: no error and no warning. After a page was read the same
// error fails the phase.
func TestScanCodeBuild_FleetsNotOffered(t *testing.T) {
	unknown := &smithy.GenericAPIError{Code: "InvalidInputException", Message: "Unknown operation ListFleets"}
	t.Run("first page", func(t *testing.T) {
		st := newTestStore(t)
		res := scanCBTopLevel(t, st, newCBFixture(), map[string]map[string]error{"ListFleets": {"": unknown}})
		if res.err != nil {
			t.Fatalf("err = %v, want nil", res.err)
		}
		if *res.warns["ListFleets"] != 0 {
			t.Errorf("ListFleets warnings = %d, want 0", *res.warns["ListFleets"])
		}
		if res.total != 5 || res.inserted != 5 {
			t.Errorf("total/inserted = %d/%d, want 5/5", res.total, res.inserted)
		}
		assertCBRows(t, st, cbldRowsWithout(TypeCodeBuildFleet))
	})
	t.Run("later page", func(t *testing.T) {
		st := newTestStore(t)
		res := scanCBTopLevel(t, st, newCBFixture(), map[string]map[string]error{"ListFleets": {"p2": unknown}})
		if !isAPIErrorCode(res.err, "InvalidInputException") {
			t.Fatalf("err = %v, want the InvalidInputException", res.err)
		}
		if res.total != 6 || res.inserted != 6 {
			t.Errorf("total/inserted = %d/%d, want 6/6", res.total, res.inserted)
		}
		assertCBRows(t, st, cbldRowsWithout(TypeCodeBuildFleet, cbldARN("fleet", "f1")))
	})
}

// A throttled fleet listing page or BatchGetFleets batch alone is returned,
// so the scan reads as transient; beside another phase's hard error it is
// demoted to one warning under the fleet phase and the hard error is
// returned. Either way the rows not lost are kept.
func TestScanCodeBuild_TransientPhaseBesideHardError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		throttle  map[string]map[string]error
		hard      bool
		want      serviceOutcome
		wantWarns int
		fleets    []string
	}{
		{"page alone", map[string]map[string]error{"ListFleets": {"p2": cbldThrottled}}, false, outcomeTransient, 0, []string{cbldARN("fleet", "f1")}},
		{"page beside a hard error", map[string]map[string]error{"ListFleets": {"p2": cbldThrottled}}, true, outcomeError, 1, []string{cbldARN("fleet", "f1")}},
		{"batch alone", map[string]map[string]error{"BatchGetFleets": {cbldARN("fleet", "f1"): cbldThrottled}}, false, outcomeTransient, 0, nil},
		{"batch beside a hard error", map[string]map[string]error{"BatchGetFleets": {cbldARN("fleet", "f1"): cbldThrottled}}, true, outcomeError, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			fail := map[string]map[string]error{}
			for op, f := range tc.throttle {
				fail[op] = f
			}
			if tc.hard {
				fail["ListSourceCredentials"] = map[string]error{"": cbldBadInput}
			}
			res := scanCBTopLevel(t, st, newCBFixture(), fail)
			if got := classifyServiceError(res.err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", res.err, got, tc.want)
			}
			wantCode := "ThrottlingException"
			if tc.hard {
				wantCode = "InvalidInputException"
			}
			if !isAPIErrorCode(res.err, wantCode) {
				t.Errorf("err = %v, want the %s", res.err, wantCode)
			}
			if tc.hard && isAPIErrorCode(res.err, "ThrottlingException") {
				t.Errorf("err = %v, want the throttle demoted out of it", res.err)
			}
			if *res.warns["ListFleets"] != tc.wantWarns {
				t.Errorf("ListFleets warnings = %d, want %d", *res.warns["ListFleets"], tc.wantWarns)
			}
			// Projects and report groups keep 2 rows each, plus the
			// source credential when its phase did not fail.
			want := len(tc.fleets) + 4
			if !tc.hard {
				want++
			}
			if res.total != want || res.inserted != want {
				t.Errorf("total/inserted = %d/%d, want %d/%d", res.total, res.inserted, want, want)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeCodeBuildFleet)); !slices.Equal(got, tc.fleets) {
				t.Errorf("fleet rows = %v, want %v", got, tc.fleets)
			}
		})
	}
}

// A failed write stops the scan with no warning claiming the lost rows, and
// no later phase runs.
func TestScanCodeBuild_WriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeCodeBuildProject)
	res := scanCBTopLevel(t, st, newCBFixture(), nil)
	if !errors.Is(res.err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", res.err)
	}
	if res.total != 2 || res.inserted != 2 {
		t.Errorf("total/inserted = %d/%d, want the fleet phase's 2/2", res.total, res.inserted)
	}
	if *res.warns["BatchGetProjects"] != 0 {
		t.Errorf("BatchGetProjects warnings = %d; want none after a failed write", *res.warns["BatchGetProjects"])
	}
	if got := res.calls.distinct(); !slices.Equal(got, []string{"BatchGetFleets", "BatchGetProjects", "ListFleets", "ListProjects"}) {
		t.Errorf("ops called = %v; want only the fleet and project ops", got)
	}
	assertCBRows(t, st, map[string][]string{TypeCodeBuildFleet: cbldAllRows[TypeCodeBuildFleet]})
}

// A context that ends after the listing leaves the batches unread; the stop
// is returned, not taken for an empty account.
func TestStoreCBBatchPhase_StoppedContextIsReturned(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, err := storeCBBatchPhase(ctx, st, newTestAccount(testAccountID), testRegion, cbBatchPhase[cbtypes.Fleet]{
		listOp: "codebuild:ListFleets", getOp: "codebuild:BatchGetFleets", what: "fleets",
		page: func(context.Context, *string) ([]string, *string, error) {
			cancel()
			return []string{"f1"}, nil, nil
		},
		get: func(context.Context, []string) ([]cbtypes.Fleet, []string, error) {
			t.Error("BatchGet called after the context ended")
			return nil, nil, nil
		},
		row: func(cbtypes.Fleet) (*store.Resource, string, string) { return nil, "", "" },
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
