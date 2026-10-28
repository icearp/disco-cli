package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/qbusiness"
	qbtypes "github.com/aws/aws-sdk-go-v2/service/qbusiness/types"
	"github.com/icearp/disco-cli/store"
)

const (
	qbTestARNPrefix = "arn:aws:qbusiness:" + testRegion + ":" + testAccountID + ":"
	// qbTestPerApp is the rows under one application: two each of data
	// accessors, indices, plugins, retrievers, web experiences, chat response
	// configurations and subscriptions, each index's two data sources, and
	// the application's chat controls.
	qbTestPerApp = 7*2 + 2*2 + 1
	// qbTestTotal is two applications with their rows.
	qbTestTotal = 2 * (1 + qbTestPerApp)
	// qbTestTagReads is every row but the subscriptions and chat controls.
	qbTestTagReads = qbTestTotal - 2*2 - 2
)

// qbTestPageCaps is the MaxResults each listing must send: the model's
// range maximum, and none (0) for ListChatResponseConfigurations, whose
// maxResults has no range.
var qbTestPageCaps = map[string]int32{
	"ListApplications": 100, "ListDataAccessors": 10, "ListIndices": 100, "ListPlugins": 50,
	"ListRetrievers": 50, "ListWebExperiences": 100, "ListChatResponseConfigurations": 0,
	"ListSubscriptions": 100, "ListDataSources": 10,
}

// qbTestRow is a row of the fixture: its type, ARN and name, the key and
// value its attributes carry, whether its tag read returns tags, and whether
// its tags are read at all.
type qbTestRow struct {
	typ, arn, name   string
	attrKey, attrVal string
	tagged, read     bool
}

// qbTestRows is every row the fixture's scan stores. Each listing serves two
// pages of one element; the first page's element has a display name and is
// tagged, the second has neither.
func qbTestRows() []qbTestRow {
	var rows []qbTestRow
	add := func(typ, arn, idKey, id string, first bool, named bool) {
		name := id
		if first && named {
			name = "dn-" + id
		}
		rows = append(rows, qbTestRow{typ: typ, arn: arn, name: name, attrKey: idKey, attrVal: id, tagged: first, read: true})
	}
	for i, app := range []string{"a1", "a2"} {
		appARN := qbTestARNPrefix + "application/" + app
		add(TypeQBusinessApplication, appARN, "ApplicationId", app, i == 0, true)
		for j, n := range []string{"1", "2"} {
			first := j == 0
			da := appARN + "/data-accessor/da" + n
			r := qbTestRow{typ: TypeQBusinessDataAccessor, arn: da, name: da, attrKey: "DataAccessorId", attrVal: "da" + n, tagged: first, read: true}
			if first {
				r.name = "dn-da1"
			}
			rows = append(rows, r)
			idx := appARN + "/index/i" + n
			add(TypeQBusinessIndex, idx, "IndexId", "i"+n, first, true)
			for k, d := range []string{"1", "2"} {
				add(TypeQBusinessDataSource, idx+"/data-source/d"+d, "DataSourceId", "d"+d, k == 0, true)
			}
			add(TypeQBusinessPlugin, appARN+"/plugin/p"+n, "PluginId", "p"+n, first, true)
			add(TypeQBusinessRetriever, appARN+"/retriever/r"+n, "RetrieverId", "r"+n, first, true)
			add(TypeQBusinessWebExperience, appARN+"/web-experience/w"+n, "WebExperienceId", "w"+n, first, false)
			add(TypeQBusinessChatResponseConfiguration, appARN+"/chat-response-configuration/c"+n, "ChatResponseConfigurationId", "c"+n, first, true)
			rows = append(rows, qbTestRow{typ: TypeQBusinessSubscription, arn: appARN + "/subscription/s" + n, name: "s" + n, attrKey: "SubscriptionId", attrVal: "s" + n})
		}
		rows = append(rows, qbTestRow{typ: TypeQBusinessChatControlsConfiguration, arn: appARN + "/chat-controls-configuration", name: app, attrKey: "ResponseScope", attrVal: string(qbtypes.ResponseScopeEnterpriseContentOnly)})
	}
	return rows
}

func qbTestTypeRows(typ string) int {
	n := 0
	for _, r := range qbTestRows() {
		if r.typ == typ {
			n++
		}
	}
	return n
}

// qbTestTagReadARNs is the ARN of every row whose tags are read, sorted.
func qbTestTagReadARNs() []string {
	var arns []string
	for _, r := range qbTestRows() {
		if r.read {
			arns = append(arns, r.arn)
		}
	}
	slices.Sort(arns)
	return arns
}

// qbCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type qbCalls struct {
	mu         sync.Mutex
	order      []string
	maxResults map[string][]int32
	parents    map[string][]string
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newQBCalls tags every tagged fixture row with its type.
func newQBCalls() *qbCalls {
	c := &qbCalls{maxResults: map[string][]int32{}, parents: map[string][]string{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, r := range qbTestRows() {
		if r.tagged {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

// record notes op's call and page size and, on a parent's first page, the
// parent: the application id, then the index id after a "/".
func (c *qbCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order = append(c.order, op)
	var n int32
	if p := bacField[*int32](in, "MaxResults"); p != nil {
		n = *p
	}
	c.maxResults[op] = append(c.maxResults[op], n)
	app := sv(bacField[*string](in, "ApplicationId"))
	if app == "" || bacField[*string](in, "NextToken") != nil {
		return
	}
	if idx := sv(bacField[*string](in, "IndexId")); idx != "" {
		app += "/" + idx
	}
	c.parents[op] = append(c.parents[op], app)
}

func (c *qbCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

func (c *qbCalls) listedParents(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.parents[op]))
}

func (c *qbCalls) listTags(in any) (any, error) {
	arn := sv(in.(*qbusiness.ListTagsForResourceInput).ResourceARN)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	var tags []qbtypes.Tag
	for k, v := range c.tags[arn] {
		tags = append(tags, qbtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &qbusiness.ListTagsForResourceOutput{Tags: tags}, nil
}

func (c *qbCalls) readARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

// twoPages answers op with page's output: first for the first page, which
// names a next one, and not first for the second, which ends the listing.
// dn is the first page's display name for id, nil on the second.
func (c *qbCalls) twoPages(op string, page func(in any, first bool, next *string, dn func(id string) *string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(op, in)
		first := bacField[*string](in, "NextToken") == nil
		var next *string
		if first {
			next = sdkaws.String("p2")
		}
		return page(in, first, next, func(id string) *string {
			if first {
				return sdkaws.String("dn-" + id)
			}
			return nil
		}), nil
	}
}

func qbTestAppOf(in any) string { return sv(bacField[*string](in, "ApplicationId")) }

// qbHandlers answers every op the scan calls with the fixture of qbTestRows.
func qbHandlers(c *qbCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApplications": c.twoPages("ListApplications", func(_ any, first bool, next *string, dn func(string) *string) any {
			id := pick(first, "a1", "a2")
			return &qbusiness.ListApplicationsOutput{NextToken: next, Applications: []qbtypes.Application{{ApplicationId: &id, DisplayName: dn(id), Status: qbtypes.ApplicationStatusActive}}}
		}),
		"ListDataAccessors": c.twoPages("ListDataAccessors", func(in any, first bool, next *string, dn func(string) *string) any {
			id := pick(first, "da1", "da2")
			arn := qbTestARNPrefix + "application/" + qbTestAppOf(in) + "/data-accessor/" + id
			return &qbusiness.ListDataAccessorsOutput{NextToken: next, DataAccessors: []qbtypes.DataAccessor{{DataAccessorId: &id, DataAccessorArn: &arn, DisplayName: dn(id)}}}
		}),
		"ListIndices": c.twoPages("ListIndices", func(_ any, first bool, next *string, dn func(string) *string) any {
			id := pick(first, "i1", "i2")
			return &qbusiness.ListIndicesOutput{NextToken: next, Indices: []qbtypes.Index{{IndexId: &id, DisplayName: dn(id)}}}
		}),
		"ListDataSources": c.twoPages("ListDataSources", func(_ any, first bool, next *string, dn func(string) *string) any {
			id := pick(first, "d1", "d2")
			return &qbusiness.ListDataSourcesOutput{NextToken: next, DataSources: []qbtypes.DataSource{{DataSourceId: &id, DisplayName: dn(id)}}}
		}),
		"ListPlugins": c.twoPages("ListPlugins", func(_ any, first bool, next *string, dn func(string) *string) any {
			id := pick(first, "p1", "p2")
			return &qbusiness.ListPluginsOutput{NextToken: next, Plugins: []qbtypes.Plugin{{PluginId: &id, DisplayName: dn(id)}}}
		}),
		"ListRetrievers": c.twoPages("ListRetrievers", func(_ any, first bool, next *string, dn func(string) *string) any {
			id := pick(first, "r1", "r2")
			return &qbusiness.ListRetrieversOutput{NextToken: next, Retrievers: []qbtypes.Retriever{{RetrieverId: &id, DisplayName: dn(id)}}}
		}),
		"ListWebExperiences": c.twoPages("ListWebExperiences", func(_ any, first bool, next *string, _ func(string) *string) any {
			id := pick(first, "w1", "w2")
			return &qbusiness.ListWebExperiencesOutput{NextToken: next, WebExperiences: []qbtypes.WebExperience{{WebExperienceId: &id}}}
		}),
		"ListChatResponseConfigurations": c.twoPages("ListChatResponseConfigurations", func(_ any, first bool, next *string, dn func(string) *string) any {
			id := pick(first, "c1", "c2")
			return &qbusiness.ListChatResponseConfigurationsOutput{NextToken: next, ChatResponseConfigurations: []qbtypes.ChatResponseConfiguration{{ChatResponseConfigurationId: &id, DisplayName: dn(id)}}}
		}),
		"ListSubscriptions": c.twoPages("ListSubscriptions", func(_ any, first bool, next *string, _ func(string) *string) any {
			id := pick(first, "s1", "s2")
			return &qbusiness.ListSubscriptionsOutput{NextToken: next, Subscriptions: []qbtypes.Subscription{{SubscriptionId: &id}}}
		}),
		"GetChatControlsConfiguration": func(in any) (any, error) {
			c.record("GetChatControlsConfiguration", in)
			return &qbusiness.GetChatControlsConfigurationOutput{ResponseScope: qbtypes.ResponseScopeEnterpriseContentOnly}, nil
		},
		"ListTagsForResource": c.listTags,
	}
}

// qbTestListOps is every list op the scan calls.
var qbTestListOps = []string{
	"ListApplications", "ListDataAccessors", "ListIndices", "ListPlugins", "ListRetrievers",
	"ListWebExperiences", "ListChatResponseConfigurations", "ListSubscriptions", "ListDataSources",
}

// qbScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func qbScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, qbDenied)
	return scanQBusiness(ctx, acct, testRegion, st, testScanID)
}

func qbWarningOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages (each child listing for every parent
// stored), each row with its name, no status, the listed element as its
// attributes, and the tags its read returns, nil for a resource without
// tags. ListTagsForResource is sent exactly the ARNs of the rows other than
// subscriptions and chat controls. Every listing sends the model's page cap.
// No error, no warning.
func TestScanQBusiness_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newQBCalls()

	total, inserted, err := qbScan(context.Background(), st, qbHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != qbTestTotal || inserted != qbTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, qbTestTotal, qbTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	byType := map[string]int{}
	for _, want := range qbTestRows() {
		byType[want.typ]++
		r, ok := rowsByNativeID(t, st, want.typ)[want.arn]
		if !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
			continue
		}
		if sv(r.Name) != want.name || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil || r.ManagedByProvider {
			t.Errorf("%s name, region, status, createdAt, managed = %q, %q, %v, %v, %v; want %q, %q, no status or createdAt, unmanaged",
				want.arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, want.name, testRegion)
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[want.attrKey] != want.attrVal {
			t.Errorf("%s attributes %s = %v (%v); want %q", want.arn, want.attrKey, attrs[want.attrKey], err, want.attrVal)
		}
		if want.tagged {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", want.arn, *r.TagsJSON)
		}
	}
	for typ, n := range byType {
		if got := len(rowsByNativeID(t, st, typ)); got != n {
			t.Errorf("%s rows = %d; want %d", typ, got, n)
		}
	}
	if got := calls.readARNs(); len(got) != qbTestTagReads || !slices.Equal(got, qbTestTagReadARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, qbTestTagReadARNs())
	}
	for _, op := range qbTestListOps[1:] {
		want := []string{"a1", "a2"}
		if op == "ListDataSources" {
			want = []string{"a1/i1", "a1/i2", "a2/i1", "a2/i2"}
		}
		if got := calls.listedParents(op); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
	}
	for _, op := range qbTestListOps {
		if calls.count(op) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range calls.maxResults[op] {
			if n != qbTestPageCaps[op] {
				t.Errorf("%s MaxResults = %d; want %d", op, n, qbTestPageCaps[op])
			}
		}
	}
}

// No application: no child listing, chat controls or tag read is made, and
// nothing is stored.
func TestScanQBusiness_EmptyListing(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newQBCalls()
	handlers := qbHandlers(calls)
	handlers["ListApplications"] = func(in any) (any, error) {
		calls.record("ListApplications", in)
		return &qbusiness.ListApplicationsOutput{}, nil
	}

	total, inserted, err := qbScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	calls.mu.Lock()
	defer calls.mu.Unlock()
	if !slices.Equal(calls.order, []string{"ListApplications"}) || len(calls.tagARNs) != 0 {
		t.Errorf("calls = %v, tag reads %v; want ListApplications only", calls.order, calls.tagARNs)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanQBusiness_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := qbScan(context.Background(), st, qbHandlers(newQBCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := qbScan(context.Background(), st, qbHandlers(newQBCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != qbTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, qbTestTotal)
	}
}

// A failed tag read. ResourceNotFoundException, which the op models, drops
// the row silently, and the children of a dropped parent are not read. A
// denial keeps the row untagged and warns once under the tag op; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanQBusiness_TagReadFailures(t *testing.T) {
	a1 := qbTestARNPrefix + "application/a1"
	for _, x := range []struct {
		arn, typ string
		children int
	}{
		{qbTestARNPrefix + "application/a2", TypeQBusinessApplication, qbTestPerApp},
		{a1 + "/index/i1", TypeQBusinessIndex, 2},
		{a1 + "/index/i2/data-source/d1", TypeQBusinessDataSource, 0},
		{a1 + "/data-accessor/da1", TypeQBusinessDataAccessor, 0},
		{a1 + "/plugin/p2", TypeQBusinessPlugin, 0},
		{a1 + "/retriever/r1", TypeQBusinessRetriever, 0},
		{a1 + "/web-experience/w2", TypeQBusinessWebExperience, 0},
		{a1 + "/chat-response-configuration/c1", TypeQBusinessChatResponseConfiguration, 0},
	} {
		for _, c := range []struct {
			name    string
			err     error
			outcome serviceOutcome // -1: no error
			code    string
			lost    int
			warnOp  string
		}{
			{"not found", qbGone, -1, "", 1 + x.children, ""},
			{"denied", qbDenied, -1, "", 0, qbTagsOp},
			{"transient", qbThrottle, outcomeTransient, "ThrottlingException", 0, ""},
			{"other error", qbBadInput, outcomeError, "ValidationException", 0, ""},
		} {
			t.Run(x.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newQBCalls()
				calls.errs[x.arn] = c.err

				total, _, err := qbScan(context.Background(), st, qbHandlers(calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				if want := qbTestTotal - c.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				r, ok := rowsByNativeID(t, st, x.typ)[x.arn]
				if ok != (c.lost == 0) {
					t.Fatalf("row stored = %v; want %v", ok, c.lost == 0)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				w := warns()
				var want []string
				if c.warnOp != "" {
					want = []string{c.warnOp}
				}
				if got := qbWarningOps(w); !slices.Equal(got, want) {
					t.Fatalf("warnings = %v; want %v", w, want)
				}
				for _, x2 := range w {
					if x2.Scope != testAccountID+"/"+testRegion || !strings.Contains(x2.Message, x.arn) {
						t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", x2.Scope, x2.Message, testAccountID, testRegion, x.arn)
					}
				}
			})
		}
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanQBusiness_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newQBCalls()
	for _, r := range qbTestRows() {
		if r.typ == TypeQBusinessApplication || r.typ == TypeQBusinessDataSource || r.typ == TypeQBusinessPlugin {
			calls.errs[r.arn] = qbDenied
		}
	}

	total, _, err := qbScan(context.Background(), st, qbHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != qbTestTotal {
		t.Errorf("total = %d; want %d", total, qbTestTotal)
	}
	if got := qbWarningOps(warns()); !slices.Equal(got, []string{qbTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, qbTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanQBusiness_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, qbTagsOp)
	calls := newQBCalls()
	calls.errs[qbTestARNPrefix+"application/a1/index/i1"] = qbThrottle
	calls.errs[qbTestARNPrefix+"application/a2/plugin/p1"] = qbBadInput

	_, _, err := qbScan(context.Background(), st, qbHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", qbTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored. A failed index write lists no data sources.
func TestScanQBusiness_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeQBusinessIndex)
	calls := newQBCalls()
	calls.errs[qbTestARNPrefix+"application/a1/index/i1"] = qbDenied

	total, _, err := qbScan(context.Background(), st, qbHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListPlugins", "ListDataSources", "ListSubscriptions", "GetChatControlsConfiguration"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
	if want := 2 + 2*2; total != want || len(rowsByNativeID(t, st, TypeQBusinessDataAccessor)) != 4 {
		t.Errorf("total = %d; want %d (the applications and their data accessors)", total, want)
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the later phases. An application not stored has no children read.
func TestScanQBusiness_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	st := newTestStore(t)
	calls := newQBCalls()
	handlers := qbHandlers(calls)
	handlers["ListApplications"] = failOnCall(2, qbBadInput, handlers["ListApplications"])

	total, _, err := qbScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if want := qbTestTotal - (1 + qbTestPerApp); total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if _, ok := rowsByNativeID(t, st, TypeQBusinessApplication)[qbTestARNPrefix+"application/a1"]; !ok {
		t.Error("application from the page before the failure missing")
	}
	if got := calls.listedParents("ListDataSources"); !slices.Equal(got, []string{"a1/i1", "a1/i2"}) {
		t.Errorf("ListDataSources parents = %v; want a1's indices", got)
	}
	if got := len(rowsByNativeID(t, st, TypeQBusinessChatControlsConfiguration)); got != 1 {
		t.Errorf("chat controls (the last phase) = %d; want 1", got)
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages and
// the later phases.
func TestScanQBusiness_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newQBCalls()
	handlers := qbHandlers(calls)
	handlers["ListPlugins"] = func(in any) (any, error) {
		calls.record("ListPlugins", in)
		id := pick(bacField[*string](in, "NextToken") == nil, "p1", "p2")
		return &qbusiness.ListPluginsOutput{NextToken: sdkaws.String("same"), Plugins: []qbtypes.Plugin{{PluginId: &id}}}, nil
	}

	total, _, err := qbScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != qbTestTotal {
		t.Errorf("total = %d; want %d", total, qbTestTotal)
	}
}

// One parent's failed child listing costs the other parents none of their
// rows: a parent deleted since it was listed is skipped silently, a denial
// warns once under the op, and any other error is returned.
func TestScanQBusiness_PerParentListingFailures(t *testing.T) {
	a1 := qbTestARNPrefix + "application/a1/"
	byApp := func(in any) bool { return qbTestAppOf(in) == "a1" }
	for _, p := range []struct {
		op, typ string
		fails   func(in any) bool
		// failed starts the NativeIDs of the failed parent's rows of typ.
		failed string
		lost   int
	}{
		{"ListDataAccessors", TypeQBusinessDataAccessor, byApp, a1, 2},
		{"ListIndices", TypeQBusinessIndex, byApp, a1, 2 + 2*2},
		{"ListPlugins", TypeQBusinessPlugin, byApp, a1, 2},
		{"ListRetrievers", TypeQBusinessRetriever, byApp, a1, 2},
		{"ListWebExperiences", TypeQBusinessWebExperience, byApp, a1, 2},
		{"ListChatResponseConfigurations", TypeQBusinessChatResponseConfiguration, byApp, a1, 2},
		{"ListSubscriptions", TypeQBusinessSubscription, byApp, a1, 2},
		{"ListDataSources", TypeQBusinessDataSource, func(in any) bool {
			return qbTestAppOf(in) == "a1" && sv(bacField[*string](in, "IndexId")) == "i1"
		}, a1 + "index/i1/", 2},
	} {
		for _, c := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"parent deleted", qbGone, "", 0},
			{"denied", qbDenied, "", 1},
			{"other error", qbBadInput, "ValidationException", 0},
		} {
			t.Run(p.op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "qbusiness:"+p.op)
				handlers := qbHandlers(newQBCalls())
				list := handlers[p.op]
				handlers[p.op] = func(in any) (any, error) {
					if p.fails(in) {
						return nil, c.err
					}
					return list(in)
				}

				total, _, err := qbScan(context.Background(), st, handlers)
				if c.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, c.code)
				}
				if want := qbTestTotal - p.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				if *warns != c.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, c.wantWarns)
				}
				rows := rowsByNativeID(t, st, p.typ)
				for arn := range rows {
					if strings.HasPrefix(arn, p.failed) {
						t.Errorf("%s stored from the failed parent", arn)
					}
				}
				if want := qbTestTypeRows(p.typ) - 2; len(rows) != want {
					t.Errorf("%s rows = %d; want %d, the other parents'", p.typ, len(rows), want)
				}
			})
		}
	}
}

// An element without the id its NativeID is built from is not stored, is not
// sent to the tag op, and is warned about once under its listing, keyed by
// its display name, another id, or the fallback text when it has none. An
// application not stored has no children read.
func TestScanQBusiness_ElementWithoutID(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		answer        any
		lost          int
	}{
		{
			"application", "ListApplications", "shop",
			&qbusiness.ListApplicationsOutput{Applications: []qbtypes.Application{{DisplayName: sdkaws.String("shop")}}}, qbTestTotal,
		},
		{
			"data accessor", "ListDataAccessors", "da9",
			&qbusiness.ListDataAccessorsOutput{DataAccessors: []qbtypes.DataAccessor{{DataAccessorId: sdkaws.String("da9")}}}, 2 * 2,
		},
		{
			"index", "ListIndices", "idx",
			&qbusiness.ListIndicesOutput{Indices: []qbtypes.Index{{DisplayName: sdkaws.String("idx")}}}, 2 * (2 + 2*2),
		},
		{
			"web experience with no endpoint", "ListWebExperiences", qbNoIdentifier,
			&qbusiness.ListWebExperiencesOutput{WebExperiences: []qbtypes.WebExperience{{}}}, 2 * 2,
		},
		{
			"subscription", "ListSubscriptions", "arn-of-sub",
			&qbusiness.ListSubscriptionsOutput{Subscriptions: []qbtypes.Subscription{{SubscriptionArn: sdkaws.String("arn-of-sub")}}}, 2 * 2,
		},
		{
			"data source", "ListDataSources", "ds",
			&qbusiness.ListDataSourcesOutput{DataSources: []qbtypes.DataSource{{DisplayName: sdkaws.String("ds")}}}, 2 * 2 * 2,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newQBCalls()
			handlers := qbHandlers(calls)
			handlers[c.op] = func(in any) (any, error) {
				calls.record(c.op, in)
				return c.answer, nil
			}

			total, _, err := qbScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := qbTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "qbusiness:"+c.op || w[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under qbusiness:%s naming %s", w, c.op, c.key)
			}
			if c.op == "ListApplications" {
				calls.mu.Lock()
				order := slices.Clone(calls.order)
				calls.mu.Unlock()
				if len(order) != 1 {
					t.Errorf("calls = %v; want ListApplications only", order)
				}
			}
			for _, arn := range calls.readARNs() {
				if strings.HasSuffix(arn, "/") || strings.HasSuffix(arn, ":") {
					t.Errorf("an ARN without an id was sent to ListTagsForResource: %s", arn)
				}
			}
		})
	}
}

// A GovCloud or China account's built ARNs, and a data accessor ARN the
// listing returns, reach the tag op unchanged.
func TestScanQBusiness_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newQBCalls()
			app := partitionARN(pf.partition, "qbusiness", pf.region, "application/a1")
			da := app + "/data-accessor/da1"
			calls.tags[app] = map[string]string{"env": "prod"}
			handlers := qbHandlers(calls)
			handlers["ListDataAccessors"] = func(in any) (any, error) {
				calls.record("ListDataAccessors", in)
				return &qbusiness.ListDataAccessorsOutput{DataAccessors: []qbtypes.DataAccessor{{DataAccessorId: sdkaws.String("da1"), DataAccessorArn: &da}}}, nil
			}
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(handlers, qbDenied)

			if _, _, err := scanQBusiness(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			read := calls.readARNs()
			for _, want := range []string{app, da, app + "/index/i1", app + "/index/i1/data-source/d2", app + "/chat-response-configuration/c1"} {
				if !slices.Contains(read, want) {
					t.Errorf("ListTagsForResource ARNs lack %s: %v", want, read)
				}
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeQBusinessApplication)[app].TagsJSON, map[string]string{"env": "prod"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanQBusiness_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newQBCalls()
	handlers := qbHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListPlugins"]
	handlers["ListPlugins"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := qbScan(ctx, st, handlers)
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
	if n := calls.count("ListRetrievers"); n != 0 {
		t.Errorf("ListRetrievers called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanQBusiness_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "qbusiness.us-east-1.api.aws", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := qbHandlers(newQBCalls())
		handlers["ListPlugins"] = func(any) (any, error) { return nil, nx }
		total, _, err := qbScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != qbTestTotal-4 {
			t.Errorf("total = %d; want the %d other rows counted", total, qbTestTotal-4)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newQBCalls()
		calls.errs[qbTestARNPrefix+"application/a2/retriever/r2"] = nx
		_, _, err := qbScan(context.Background(), st, qbHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanQBusiness(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// Each child phase runs after the phase that lists its parents, and the
// chat controls phase still runs last: when an op is first called, every op
// listed before it has been called.
func TestScanQBusiness_PhaseOrder(t *testing.T) {
	st := newTestStore(t)
	calls := newQBCalls()
	if _, _, err := qbScan(context.Background(), st, qbHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	calls.mu.Lock()
	defer calls.mu.Unlock()
	var firsts []string
	for _, op := range calls.order {
		if !slices.Contains(firsts, op) {
			firsts = append(firsts, op)
		}
	}
	want := []string{
		"ListApplications", "ListDataAccessors", "ListIndices", "ListPlugins", "ListRetrievers", "ListWebExperiences",
		"ListChatResponseConfigurations", "ListSubscriptions", "ListDataSources", "GetChatControlsConfiguration",
	}
	if !slices.Equal(firsts, want) {
		t.Errorf("ops in first-call order = %v; want %v", firsts, want)
	}
	// Every call of a phase precedes the next phase's first.
	last := map[string]int{}
	for i, op := range calls.order {
		last[op] = i
	}
	for i := 1; i < len(want); i++ {
		if last[want[i-1]] > slices.Index(calls.order, want[i]) {
			t.Errorf("%s called after %s started", want[i-1], want[i])
		}
	}
}

// The status ListApplications returns reaches the chat controls phase: a
// FAILED application's chat controls are not read, its other rows are.
func TestScanQBusiness_FailedApplicationHasNoChatControls(t *testing.T) {
	st := newTestStore(t)
	calls := newQBCalls()
	handlers := qbHandlers(calls)
	list := handlers["ListApplications"]
	handlers["ListApplications"] = func(in any) (any, error) {
		out, err := list(in)
		if a := out.(*qbusiness.ListApplicationsOutput).Applications; sv(a[0].ApplicationId) == "a2" {
			a[0].Status = qbtypes.ApplicationStatusFailed
		}
		return out, err
	}

	total, _, err := qbScan(context.Background(), st, handlers)
	if err != nil || total != qbTestTotal-1 {
		t.Fatalf("total, err = %d, %v; want %d, nil", total, err, qbTestTotal-1)
	}
	if got := calls.listedParents("GetChatControlsConfiguration"); !slices.Equal(got, []string{"a1"}) {
		t.Errorf("chat controls read for %v; want [a1]", got)
	}
}

// A chat response configuration's tag read sends the ARN its listing
// returned, even where it differs from the built NativeID, and the built
// NativeID when the listing returned none. The NativeID stays built.
func TestScanQBusiness_ChatResponseConfigurationTagReadSendsListedARN(t *testing.T) {
	st := newTestStore(t)
	calls := newQBCalls()
	built := qbTestARNPrefix + "application/a1/chat-response-configuration/c1"
	listed := qbTestARNPrefix + "application/a1/chat-response-configuration/listed-c1"
	calls.tags[listed] = map[string]string{"from": "listed"}
	handlers := qbHandlers(calls)
	list := handlers["ListChatResponseConfigurations"]
	handlers["ListChatResponseConfigurations"] = func(in any) (any, error) {
		out, err := list(in)
		if c := out.(*qbusiness.ListChatResponseConfigurationsOutput).ChatResponseConfigurations; qbTestAppOf(in) == "a1" && sv(c[0].ChatResponseConfigurationId) == "c1" {
			c[0].ChatResponseConfigurationArn = &listed
		}
		return out, err
	}

	if _, _, err := qbScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	read := calls.readARNs()
	if !slices.Contains(read, listed) || slices.Contains(read, built) {
		t.Errorf("ListTagsForResource ARNs = %v; want %s and not %s", read, listed, built)
	}
	if !slices.Contains(read, qbTestARNPrefix+"application/a1/chat-response-configuration/c2") {
		t.Errorf("ListTagsForResource ARNs = %v; want the built ARN of c2, listed without one", read)
	}
	r, ok := rowsByNativeID(t, st, TypeQBusinessChatResponseConfiguration)[built]
	if !ok {
		t.Fatalf("row %s missing", built)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"from": "listed"})
}
