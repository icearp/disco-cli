package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/icearp/disco-cli/store"
)

var (
	cwDenied = apiErr("AccessDenied", "User: arn:aws:iam::123:user/x is not authorized to perform: cloudwatch:List")
	// cwThrottle is a code isTransientNetworkError accepts.
	cwThrottle = apiErr("Throttling", "Rate exceeded")
	// cwInvalid is a code ListTagsForResource, GetDashboard and
	// GetMetricStream model that the scan neither skips nor tolerates.
	cwInvalid = apiErr("InvalidParameterValue", "bad")
)

// cwTestTotal is the rows the fixture's regional scan stores: two of each
// regional tagged kind and the OTel enrichment singleton.
const cwTestTotal = 5*2 + 1

// cwKind is one tagged type: the op that lists it, the page-size field and
// value that op must send ("" for none), and elem, which builds one listed
// element under arn named name. An insight rule's element carries no ARN;
// its arn is the one the scan builds.
type cwKind struct {
	op, typ, arnPrefix string
	pageField          string
	pageSize           int32
	elem               func(arn, name string, tagged bool) any
}

func (k cwKind) name(suffix string) string {
	_, kind, _ := strings.Cut(k.typ, "aws:cloudwatch:")
	return kind + "-" + suffix
}

// arn is the row's ARN in the Service Reference's format for k's type.
func (k cwKind) arn(suffix string) string { return k.arnPrefix + k.name(suffix) }

// global reports whether k's rows are stored by the once-per-account
// dashboards scan rather than the regional scan.
func (k cwKind) global() bool { return k.typ == TypeCloudWatchDashboard }

// scan runs the real entry point that stores k's rows against handlers.
func (k cwKind) scan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	if k.global() {
		return cwDashboardScan(ctx, st, handlers)
	}
	return cwScan(ctx, st, handlers)
}

// scanTotal is the rows the fixture's scan of k stores.
func (k cwKind) scanTotal() int {
	if k.global() {
		return 2
	}
	return cwTestTotal
}

// region is the Region k's rows are stored under.
func (k cwKind) region() string {
	if k.global() {
		return "global"
	}
	return testRegion
}

// scope is the Scope k's scan warns under: the account alone for a global
// scan.
func (k cwKind) scope() string {
	if k.global() {
		return testAccountID
	}
	return testAccountID + "/" + testRegion
}

// rowName is the Name a row of k is stored under: an alarm mute rule has no
// name and is named by its ARN.
func (k cwKind) rowName(suffix string) string {
	if k.typ == TypeCloudWatchAlarmMuteRule {
		return k.arn(suffix)
	}
	return k.name(suffix)
}

// cwRegionalARN is the fixture's commercial CloudWatch ARN in testRegion
// whose resource part is rest.
func cwRegionalARN(rest string) string {
	return "arn:aws:cloudwatch:" + testRegion + ":" + testAccountID + ":" + rest
}

// cwDashboardARN is the dashboard name's ARN: the Service Reference's format
// carries no region.
func cwDashboardARN(name string) string {
	return "arn:aws:cloudwatch::" + testAccountID + ":dashboard/" + name
}

func cwKinds() []cwKind {
	regional := cwRegionalARN("")
	return []cwKind{
		{
			op: "DescribeAlarms", typ: TypeCloudWatchAlarm, arnPrefix: regional + "alarm:", pageField: "MaxRecords", pageSize: 100,
			elem: func(arn, name string, _ bool) any {
				return cwtypes.MetricAlarm{AlarmArn: &arn, AlarmName: &name, Namespace: sdkaws.String("AWS/EC2"), MetricName: sdkaws.String("CPUUtilization")}
			},
		},
		{
			op: "DescribeAlarms", typ: TypeCloudWatchCompositeAlarm, arnPrefix: regional + "alarm:", pageField: "MaxRecords", pageSize: 100,
			elem: func(arn, name string, _ bool) any {
				return cwtypes.CompositeAlarm{AlarmArn: &arn, AlarmName: &name, AlarmRule: sdkaws.String("ALARM(x)")}
			},
		},
		{
			op: "ListAlarmMuteRules", typ: TypeCloudWatchAlarmMuteRule, arnPrefix: regional + "alarm-mute-rule:", pageField: "MaxRecords", pageSize: 100,
			elem: func(arn, _ string, _ bool) any {
				return cwtypes.AlarmMuteRuleSummary{AlarmMuteRuleArn: &arn, MuteType: sdkaws.String("ONE_TIME"), Status: cwtypes.AlarmMuteRuleStatusActive}
			},
		},
		{
			op: "ListDashboards", typ: TypeCloudWatchDashboard, arnPrefix: cwDashboardARN(""),
			elem: func(arn, name string, _ bool) any {
				return cwtypes.DashboardEntry{DashboardArn: &arn, DashboardName: &name, Size: sdkaws.Int64(42)}
			},
		},
		{
			op: "DescribeInsightRules", typ: TypeCloudWatchInsightRule, arnPrefix: regional + "insight-rule/", pageField: "MaxResults", pageSize: 500,
			// The tagged rule is a managed one: its tags are read too.
			elem: func(_, name string, tagged bool) any {
				return cwtypes.InsightRule{Name: &name, State: sdkaws.String("ENABLED"), Schema: sdkaws.String("CloudWatchLogRule/1"), Definition: sdkaws.String("{}"), ManagedRule: sdkaws.Bool(tagged)}
			},
		},
		{
			op: "ListMetricStreams", typ: TypeCloudWatchMetricStream, arnPrefix: regional + "metric-stream/", pageField: "MaxResults", pageSize: 500,
			elem: func(arn, name string, _ bool) any {
				return cwtypes.MetricStreamEntry{Arn: &arn, Name: &name, FirehoseArn: sdkaws.String("arn:aws:firehose:us-east-1:123456789012:deliverystream/fh"), State: sdkaws.String("running")}
			},
		},
	}
}

func cwKindOf(t *testing.T, typ string) cwKind {
	t.Helper()
	for _, k := range cwKinds() {
		if k.typ == typ {
			return k
		}
	}
	t.Fatalf("%s is no tagged CloudWatch type", typ)
	return cwKind{}
}

// cwListOutput is op's output holding elems, with next.
func cwListOutput(op string, elems []any, next *string) any {
	switch op {
	case "DescribeAlarms":
		out := &cloudwatch.DescribeAlarmsOutput{NextToken: next}
		for _, e := range elems {
			switch a := e.(type) {
			case cwtypes.MetricAlarm:
				out.MetricAlarms = append(out.MetricAlarms, a)
			case cwtypes.CompositeAlarm:
				out.CompositeAlarms = append(out.CompositeAlarms, a)
			}
		}
		return out
	case "ListAlarmMuteRules":
		out := &cloudwatch.ListAlarmMuteRulesOutput{NextToken: next}
		for _, e := range elems {
			out.AlarmMuteRuleSummaries = append(out.AlarmMuteRuleSummaries, e.(cwtypes.AlarmMuteRuleSummary))
		}
		return out
	case "ListDashboards":
		out := &cloudwatch.ListDashboardsOutput{NextToken: next}
		for _, e := range elems {
			out.DashboardEntries = append(out.DashboardEntries, e.(cwtypes.DashboardEntry))
		}
		return out
	case "DescribeInsightRules":
		out := &cloudwatch.DescribeInsightRulesOutput{NextToken: next}
		for _, e := range elems {
			out.InsightRules = append(out.InsightRules, e.(cwtypes.InsightRule))
		}
		return out
	default:
		out := &cloudwatch.ListMetricStreamsOutput{NextToken: next}
		for _, e := range elems {
			out.Entries = append(out.Entries, e.(cwtypes.MetricStreamEntry))
		}
		return out
	}
}

// cwCalls records the calls of a scan and answers ListTagsForResource with
// tags, or errs, by ARN.
type cwCalls struct {
	mu      sync.Mutex
	inputs  map[string][]any
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newCWCalls tags the "tagged" row of every kind with its type.
func newCWCalls() *cwCalls {
	c := &cwCalls{inputs: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range cwKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *cwCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs[op] = append(c.inputs[op], in)
}

func (c *cwCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inputs[op])
}

func (c *cwCalls) listTags(in any) (any, error) {
	arn := sv(in.(*cloudwatch.ListTagsForResourceInput).ResourceARN)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []cwtypes.Tag
	for k, v := range tags {
		list = append(list, cwtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &cloudwatch.ListTagsForResourceOutput{Tags: list}, nil
}

// twoPages answers op with every kind it lists: "tagged" on the first page
// and "bare" on the second.
func (c *cwCalls) twoPages(op string) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		var elems []any
		for _, k := range cwKinds() {
			if k.op == op {
				elems = append(elems, k.elem(k.arn(suffix), k.name(suffix), suffix == "tagged"))
			}
		}
		return cwListOutput(op, elems, next), nil
	}
}

// cwDashboardBody is the GetDashboard body of the dashboard name: it carries
// the DashboardBody the listed entry lacks.
func cwDashboardBody(name string) *cloudwatch.GetDashboardOutput {
	return &cloudwatch.GetDashboardOutput{
		DashboardArn:  sdkaws.String(cwDashboardARN(name)),
		DashboardName: &name, DashboardBody: sdkaws.String(`{"widgets":[]}`),
	}
}

// cwStreamBody is the GetMetricStream body of the stream name: it carries
// the RoleArn the listed entry lacks.
func cwStreamBody(name string) *cloudwatch.GetMetricStreamOutput {
	return &cloudwatch.GetMetricStreamOutput{
		Arn: sdkaws.String(cwRegionalARN("metric-stream/" + name)), Name: &name,
		RoleArn: sdkaws.String("arn:aws:iam::123456789012:role/stream"), State: sdkaws.String("running"),
	}
}

func (c *cwCalls) getDashboard(in any) (any, error) {
	c.record("GetDashboard", in)
	return cwDashboardBody(sv(in.(*cloudwatch.GetDashboardInput).DashboardName)), nil
}

func (c *cwCalls) getMetricStream(in any) (any, error) {
	c.record("GetMetricStream", in)
	return cwStreamBody(sv(in.(*cloudwatch.GetMetricStreamInput).Name)), nil
}

// cwHandlers answers every CloudWatch op the scan calls: each tagged kind in
// two pages, no anomaly detectors, and the OTel enrichment singleton.
func cwHandlers(c *cwCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"GetDashboard":        c.getDashboard,
		"GetMetricStream":     c.getMetricStream,
		"DescribeAnomalyDetectors": func(in any) (any, error) {
			c.record("DescribeAnomalyDetectors", in)
			return &cloudwatch.DescribeAnomalyDetectorsOutput{}, nil
		},
		"GetOTelEnrichment": func(in any) (any, error) {
			c.record("GetOTelEnrichment", in)
			return &cloudwatch.GetOTelEnrichmentOutput{Status: cwtypes.OTelEnrichmentStatusRunning}, nil
		},
	}
	for _, k := range cwKinds() {
		h[k.op] = c.twoPages(k.op)
	}
	return h
}

// cwScan runs the regional scan's real entry point against handlers; every
// op they do not answer is denied.
func cwScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, cwDenied)
	return scanCloudWatch(ctx, acct, testRegion, st, testScanID)
}

// cwDashboardScan runs the dashboards' real entry point against handlers;
// every op they do not answer is denied.
func cwDashboardScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, cwDenied)
	return scanCloudWatchDashboards(ctx, acct, st, testScanID)
}

// cwTagReadARNs is the ARN of every row whose tags the fixture's scan reads:
// all of them.
func cwTagReadARNs() []string {
	var arns []string
	for _, k := range cwKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

// cwWantAttrs is the attributes a fixture row of k is stored with: the
// detail body for a dashboard or metric stream, else the listed element.
func cwWantAttrs(k cwKind, suffix string) string {
	switch k.typ {
	case TypeCloudWatchDashboard:
		return mustJSON(cwDashboardBody(k.name(suffix)))
	case TypeCloudWatchMetricStream:
		return mustJSON(cwStreamBody(k.name(suffix)))
	}
	return mustJSON(k.elem(k.arn(suffix), k.name(suffix), suffix == "tagged"))
}

// Every tagged listing stores both its pages, each row under its ARN with
// its Name and attributes (a dashboard's and a metric stream's from its Get
// body), no Status or CreatedAt, unmanaged, and the tags ListTagsForResource
// returns for that ARN, nil for a resource without tags. Regional rows are
// stored under the scanned region and dashboards under the global region, by
// the dashboards scan alone. Exactly those ARNs are sent to the tag op; each
// listing sends its model's page cap, and DescribeAlarms asks for both alarm
// types. No error, no warning.
func TestScanCloudWatch_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCWCalls()
	handlers := cwHandlers(calls)

	total, inserted, err := cwScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("regional scan: %v", err)
	}
	if total != cwTestTotal || inserted != cwTestTotal {
		t.Errorf("regional total, inserted = %d, %d; want %d, %d", total, inserted, cwTestTotal, cwTestTotal)
	}
	for _, op := range []string{"ListDashboards", "GetDashboard"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("regional scan called %s %d times; want 0", op, n)
		}
	}
	total, inserted, err = cwDashboardScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("dashboards scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("dashboards total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range cwKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %v; want 2", k.typ, nativeIDs(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.rowName(suffix) || sv(r.Region) != k.region() || r.Status != nil || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %q, %q, %v, %v, %v; want %q, %q, no Status or CreatedAt, false",
					arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, k.rowName(suffix), k.region())
			}
			assertJSONEqual(t, arn+" attrs", r.AttributesJSON, cwWantAttrs(k, suffix))
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		for _, in := range calls.inputs[k.op] {
			n := bacField[*int32](in, k.pageField)
			if k.pageField != "" && (n == nil || *n != k.pageSize) {
				t.Errorf("%s %s = %v; want %d", k.op, k.pageField, n, k.pageSize)
			}
		}
		if n := calls.count(k.op); n != 2 {
			t.Errorf("%s calls = %d; want 2", k.op, n)
		}
	}
	for _, in := range calls.inputs["DescribeAlarms"] {
		types := in.(*cloudwatch.DescribeAlarmsInput).AlarmTypes
		if !slices.Equal(types, []cwtypes.AlarmType{cwtypes.AlarmTypeMetricAlarm, cwtypes.AlarmTypeCompositeAlarm}) {
			t.Errorf("DescribeAlarms AlarmTypes = %v; want both", types)
		}
	}
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, cwTagReadARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, cwTagReadARNs())
	}
	var gets []string
	for _, in := range calls.inputs["GetDashboard"] {
		gets = append(gets, sv(in.(*cloudwatch.GetDashboardInput).DashboardName))
	}
	for _, in := range calls.inputs["GetMetricStream"] {
		gets = append(gets, sv(in.(*cloudwatch.GetMetricStreamInput).Name))
	}
	slices.Sort(gets)
	if want := []string{"dashboard-bare", "dashboard-tagged", "metric-stream-bare", "metric-stream-tagged"}; !slices.Equal(gets, want) {
		t.Errorf("GetDashboard and GetMetricStream names = %v; want %v", gets, want)
	}
}

// Every listing empty: only the OTel singleton is stored, no tag read, no
// error, no warning.
func TestScanCloudWatch_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newCWCalls()
	handlers := cwHandlers(calls)
	for _, k := range cwKinds() {
		handlers[k.op] = func(any) (any, error) { return cwListOutput(k.op, nil, nil), nil }
	}
	total, inserted, err := cwScan(context.Background(), st, handlers)
	if err != nil || total != 1 || inserted != 1 {
		t.Errorf("regional total, inserted, err = %d, %d, %v; want 1, 1, nil", total, inserted, err)
	}
	total, inserted, err = cwDashboardScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("dashboards total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	for _, op := range []string{"ListTagsForResource", "GetDashboard", "GetMetricStream"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s calls = %d; want 0", op, n)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanCloudWatch_RescanInsertsNone(t *testing.T) {
	for _, k := range []cwKind{cwKindOf(t, TypeCloudWatchAlarm), cwKindOf(t, TypeCloudWatchDashboard)} {
		t.Run(k.typ, func(t *testing.T) {
			st := newTestStore(t)
			if _, _, err := k.scan(context.Background(), st, cwHandlers(newCWCalls())); err != nil {
				t.Fatalf("first scan: %v", err)
			}
			total, inserted, err := k.scan(context.Background(), st, cwHandlers(newCWCalls()))
			if err != nil {
				t.Fatalf("rescan: %v", err)
			}
			if total != k.scanTotal() || inserted != 0 {
				t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, k.scanTotal())
			}
		})
	}
}

// A failed tag read: ResourceNotFoundException drops the row silently; a
// denial keeps the row untagged and warns once under the tag op; a throttle
// keeps it and is transient; any other modeled code keeps it and is an error.
func TestScanCloudWatch_TagReadFailures(t *testing.T) {
	type tagCase struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		wantWarns int
	}
	var cases []tagCase
	for _, k := range cwKinds() {
		arn := k.arn("tagged")
		cases = append(cases,
			tagCase{k.typ + "/not found", arn, k.typ, apiErr("ResourceNotFoundException", "gone"), -1, "", k.scanTotal() - 1, false, 0},
			tagCase{k.typ + "/denied", arn, k.typ, cwDenied, -1, "", k.scanTotal(), true, 1},
			tagCase{k.typ + "/transient", arn, k.typ, cwThrottle, outcomeTransient, "Throttling", k.scanTotal(), true, 0},
			tagCase{k.typ + "/other error", arn, k.typ, cwInvalid, outcomeError, "InvalidParameterValue", k.scanTotal(), true, 0},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newCWCalls()
			calls.errs[c.arn] = c.err
			k := cwKindOf(t, c.typ)

			total, _, err := k.scan(context.Background(), st, cwHandlers(calls))
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
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			if len(w) != c.wantWarns {
				t.Fatalf("warnings = %v; want %d", w, c.wantWarns)
			}
			for _, x := range w {
				if x.Service != cwTagsOp || x.Scope != k.scope() || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning %+v; want one under %s in %s naming %s", x, cwTagsOp, k.scope(), c.arn)
				}
			}
		})
	}
}

// Denied tag reads in two listings send one warning for the whole scan,
// under the tag op, naming the first denied row; the rows are kept untagged.
func TestScanCloudWatch_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, cwTagsOp)
	calls := newCWCalls()
	typs := []string{TypeCloudWatchAlarmMuteRule, TypeCloudWatchMetricStream}
	for _, typ := range typs {
		k := cwKindOf(t, typ)
		calls.errs[k.arn("tagged")] = cwDenied
		calls.errs[k.arn("bare")] = cwDenied
	}

	total, _, err := cwScan(context.Background(), st, cwHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != cwTestTotal {
		t.Errorf("total = %d; want %d", total, cwTestTotal)
	}
	first := cwKindOf(t, TypeCloudWatchAlarmMuteRule).arn("tagged")
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion || !strings.Contains((*warnings)[0].Message, first) {
		t.Fatalf("%s warnings = %v; want 1 in %s/%s naming %s", cwTagsOp, *warnings, testAccountID, testRegion, first)
	}
	for _, typ := range typs {
		k := cwKindOf(t, typ)
		for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
			if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v; want stored untagged", arn, ok)
			}
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanCloudWatch_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, cwTagsOp)
	calls := newCWCalls()
	calls.errs[cwKindOf(t, TypeCloudWatchAlarm).arn("tagged")] = cwThrottle
	calls.errs[cwKindOf(t, TypeCloudWatchMetricStream).arn("bare")] = cwInvalid

	_, _, err := cwScan(context.Background(), st, cwHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterValue")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", cwTagsOp, *warns)
	}
}

// A throttled listing beside a later phase's hard error is demoted to one
// warning under its own op; the hard error decides the outcome.
func TestScanCloudWatch_ListThrottleDemotedBesideLaterHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, cwDescribeAlarmsOp)
	handlers := cwHandlers(newCWCalls())
	handlers["DescribeAlarms"] = func(any) (any, error) { return nil, cwThrottle }
	handlers["ListMetricStreams"] = func(any) (any, error) { return nil, cwInvalid }

	total, _, err := cwScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterValue")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", cwDescribeAlarmsOp, *warns)
	}
	if want := cwTestTotal - 4 - 2; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
}

// Each paged listing writes its first page before reading the next, and a
// failed write stops the scan: the listing reads no further page, no later
// phase runs and no warning is sent about rows the write lost (here a denied
// tag read); the phases before it keep their rows.
func TestScanCloudWatch_FailedWriteStopsScan(t *testing.T) {
	for _, c := range []struct {
		typ    string
		later  []string
		before int
	}{
		{TypeCloudWatchAlarm, []string{"ListAlarmMuteRules", "DescribeAnomalyDetectors", "DescribeInsightRules", "ListMetricStreams", "GetOTelEnrichment"}, 0},
		// Alarms (2 metric, 2 composite) ran before it.
		{TypeCloudWatchAlarmMuteRule, []string{"DescribeAnomalyDetectors", "DescribeInsightRules", "ListMetricStreams", "GetOTelEnrichment"}, 4},
		// Alarms and mute rules (2) ran before it; the anomaly detectors stored none.
		{TypeCloudWatchInsightRule, []string{"ListMetricStreams", "GetOTelEnrichment"}, 6},
		{TypeCloudWatchMetricStream, []string{"GetOTelEnrichment"}, 8},
		// The dashboards scan has no other phase.
		{TypeCloudWatchDashboard, nil, 0},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			calls := newCWCalls()
			k := cwKindOf(t, c.typ)
			calls.errs[k.arn("tagged")] = cwDenied

			total, _, err := k.scan(context.Background(), st, cwHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			if n := calls.count(k.op); n != 1 {
				t.Errorf("%s called %d times; want 1, none after the failed write", k.op, n)
			}
			for _, op := range c.later {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times after the failed write; want 0", op, n)
				}
			}
			if total != c.before {
				t.Errorf("total = %d; want %d", total, c.before)
			}
		})
	}
}

// A listing's failed page keeps the page read before it, and its error does
// not stop the later listings (the dashboards scan has none).
func TestScanCloudWatch_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		typ        string
		lost, last int
	}{
		{TypeCloudWatchAlarm, 2, 1},
		{TypeCloudWatchDashboard, 1, 0},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			calls := newCWCalls()
			handlers := cwHandlers(calls)
			k := cwKindOf(t, c.typ)
			handlers[k.op] = failOnCall(2, cwInvalid, handlers[k.op])

			total, _, err := k.scan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidParameterValue")
			if want := k.scanTotal() - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
				t.Errorf("%s from the page before the failure missing", k.typ)
			}
			if n := calls.count("GetOTelEnrichment"); n != c.last {
				t.Errorf("GetOTelEnrichment calls = %d; want %d", n, c.last)
			}
		})
	}
}

// A repeated token ends a listing as an error, keeping its pages.
func TestScanCloudWatch_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := cwHandlers(newCWCalls())
	k := cwKindOf(t, TypeCloudWatchInsightRule)
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		return cwListOutput(k.op, []any{k.elem("", k.name(suffix), false)}, sdkaws.String("same")), nil
	}
	total, _, err := cwScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != cwTestTotal {
		t.Errorf("total = %d; want %d", total, cwTestTotal)
	}
}

// An element without an ARN (an insight rule: without a name) is not
// stored, is not sent to the tag op, and is warned about once under its
// listing, named by its name or by the fallback text when it has none.
func TestScanCloudWatch_ElementWithoutIdentifierWarns(t *testing.T) {
	for _, c := range []struct {
		typ, key string
		elem     any
	}{
		{TypeCloudWatchAlarm, "nameless-arn", cwtypes.MetricAlarm{AlarmName: sdkaws.String("nameless-arn")}},
		{TypeCloudWatchCompositeAlarm, "nameless-arn", cwtypes.CompositeAlarm{AlarmName: sdkaws.String("nameless-arn")}},
		{TypeCloudWatchAlarmMuteRule, noIdentifier, cwtypes.AlarmMuteRuleSummary{}},
		{TypeCloudWatchDashboard, "nameless-arn", cwtypes.DashboardEntry{DashboardName: sdkaws.String("nameless-arn")}},
		{TypeCloudWatchInsightRule, noIdentifier, cwtypes.InsightRule{State: sdkaws.String("ENABLED")}},
		{TypeCloudWatchMetricStream, "nameless-arn", cwtypes.MetricStreamEntry{Name: sdkaws.String("nameless-arn")}},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newCWCalls()
			handlers := cwHandlers(calls)
			k := cwKindOf(t, c.typ)
			handlers[k.op] = func(any) (any, error) { return cwListOutput(k.op, []any{c.elem}, nil), nil }

			total, _, err := k.scan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			lost := 2
			if k.op == "DescribeAlarms" {
				lost = 4
			}
			if want := k.scanTotal() - lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 {
				t.Fatalf("warnings = %v; want 1", w)
			}
			// warnNotStored scopes a global listing's warning account/global,
			// unlike skipIfAccessDenied (scope).
			if w[0].Service != "cloudwatch:"+k.op || !strings.Contains(w[0].Message, c.key) || w[0].Scope != testAccountID+"/"+k.region() {
				t.Errorf("warning %+v; want one under cloudwatch:%s naming %q in %s/%s", w[0], k.op, c.key, testAccountID, k.region())
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 0 {
				t.Errorf("%s rows = %d; want 0", c.typ, n)
			}
			if slices.ContainsFunc(calls.tagARNs, func(arn string) bool { return arn == "" || strings.HasSuffix(arn, "/") }) {
				t.Errorf("ListTagsForResource ARNs %v hold one without an identifier", calls.tagARNs)
			}
		})
	}
}

// In the aws-us-gov and aws-cn partitions a listed ARN (an alarm's, a
// dashboard's) reaches the tag op unchanged, and an insight rule is stored
// and read under the ARN built in the Service Reference's format with the
// account's partition.
func TestScanCloudWatch_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newCWCalls()
			handlers := cwHandlers(calls)
			alarm := "arn:" + c.partition + ":cloudwatch:" + c.region + ":" + testAccountID + ":alarm:cpu"
			rule := "arn:" + c.partition + ":cloudwatch:" + c.region + ":" + testAccountID + ":insight-rule/top-talkers"
			dashboard := "arn:" + c.partition + ":cloudwatch::" + testAccountID + ":dashboard/ops"
			calls.tags[alarm] = map[string]string{"k": "alarm"}
			calls.tags[rule] = map[string]string{"k": "rule"}
			calls.tags[dashboard] = map[string]string{"k": "dashboard"}
			handlers["DescribeAlarms"] = func(any) (any, error) {
				return &cloudwatch.DescribeAlarmsOutput{MetricAlarms: []cwtypes.MetricAlarm{{AlarmArn: &alarm, AlarmName: sdkaws.String("cpu")}}}, nil
			}
			handlers["DescribeInsightRules"] = func(any) (any, error) {
				return &cloudwatch.DescribeInsightRulesOutput{InsightRules: []cwtypes.InsightRule{{Name: sdkaws.String("top-talkers")}}}, nil
			}
			handlers["ListDashboards"] = func(any) (any, error) {
				return &cloudwatch.ListDashboardsOutput{DashboardEntries: []cwtypes.DashboardEntry{{DashboardArn: &dashboard, DashboardName: sdkaws.String("ops")}}}, nil
			}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(handlers, cwDenied)
			if _, _, err := scanCloudWatch(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("regional scan: %v", err)
			}
			if _, _, err := scanCloudWatchDashboards(context.Background(), acct, st, testScanID); err != nil {
				t.Fatalf("dashboards scan: %v", err)
			}
			for _, arn := range []string{alarm, rule, dashboard} {
				if !slices.Contains(calls.tagARNs, arn) {
					t.Errorf("ListTagsForResource ARNs %v lack %s", calls.tagARNs, arn)
				}
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeCloudWatchAlarm)[alarm].TagsJSON, map[string]string{"k": "alarm"})
			assertTagsMap(t, rowsByNativeID(t, st, TypeCloudWatchInsightRule)[rule].TagsJSON, map[string]string{"k": "rule"})
			assertTagsMap(t, rowsByNativeID(t, st, TypeCloudWatchDashboard)[dashboard].TagsJSON, map[string]string{"k": "dashboard"})
		})
	}
}

// An NXDOMAIN on the first listing, nothing stored, leaves the region
// unavailable and runs no later phase; one after rows were stored is a
// failure of this scan that keeps their counts.
func TestScanCloudWatch_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "monitoring.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newCWCalls()
		handlers := cwHandlers(calls)
		handlers["DescribeAlarms"] = func(any) (any, error) { return nil, nx }
		total, _, err := cwScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 {
			t.Errorf("total = %d; want 0", total)
		}
		if n := calls.count("ListAlarmMuteRules"); n != 0 {
			t.Errorf("ListAlarmMuteRules calls = %d; want 0", n)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
	t.Run("after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := cwHandlers(newCWCalls())
		handlers["ListMetricStreams"] = func(any) (any, error) { return nil, nx }
		total, _, err := cwScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != cwTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, cwTestTotal-2)
		}
	})
}

// A failed GetDashboard or GetMetricStream: a not-found code the op models
// drops the row silently with no tag read; any other failure keeps the
// listed element as the row, its tags read: a denial warns once under the
// Get op, a throttle is transient, any other error is an error.
func TestScanCloudWatch_DetailFailures(t *testing.T) {
	type detailCase struct {
		name      string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		kept      bool
		wantWarns int
	}
	kept := []detailCase{
		{"denied", cwDenied, -1, "", true, 1},
		{"throttled", cwThrottle, outcomeTransient, "Throttling", true, 0},
		{"other error", cwInvalid, outcomeError, "InvalidParameterValue", true, 0},
	}
	for _, d := range []struct {
		typ, op  string
		notFound []string
		name     func(in any) string
	}{
		{TypeCloudWatchDashboard, "GetDashboard", []string{"ResourceNotFound", "DashboardNotFoundError"}, func(in any) string {
			return sv(in.(*cloudwatch.GetDashboardInput).DashboardName)
		}},
		{TypeCloudWatchMetricStream, "GetMetricStream", []string{"ResourceNotFoundException"}, func(in any) string {
			return sv(in.(*cloudwatch.GetMetricStreamInput).Name)
		}},
	} {
		cases := slices.Clone(kept)
		for _, code := range d.notFound {
			cases = append(cases, detailCase{"not found " + code, apiErr(code, "gone"), -1, "", false, 0})
		}
		for _, c := range cases {
			t.Run(d.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newCWCalls()
				handlers := cwHandlers(calls)
				k := cwKindOf(t, d.typ)
				failing := k.arn("tagged")
				next := handlers[d.op]
				handlers[d.op] = func(in any) (any, error) {
					if d.name(in) == k.name("tagged") {
						return nil, c.err
					}
					return next(in)
				}

				total, _, err := k.scan(context.Background(), st, handlers)
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				r, ok := rowsByNativeID(t, st, d.typ)[failing]
				if ok != c.kept {
					t.Fatalf("row stored = %v; want %v", ok, c.kept)
				}
				wantTotal := k.scanTotal()
				if !c.kept {
					wantTotal--
					if slices.Contains(calls.tagARNs, failing) {
						t.Errorf("%s sent to ListTagsForResource", failing)
					}
				} else {
					if sv(r.Name) != k.name("tagged") {
						t.Errorf("Name = %q; want %q", sv(r.Name), k.name("tagged"))
					}
					assertJSONEqual(t, failing+" attrs (the listed element)", r.AttributesJSON, mustJSON(k.elem(failing, k.name("tagged"), true)))
					assertTagsMap(t, r.TagsJSON, map[string]string{"type": d.typ})
				}
				if total != wantTotal {
					t.Errorf("total = %d; want %d", total, wantTotal)
				}
				w := warns()
				if len(w) != c.wantWarns {
					t.Fatalf("warnings = %v; want %d", w, c.wantWarns)
				}
				for _, x := range w {
					if x.Service != "cloudwatch:"+d.op || !strings.Contains(x.Message, failing) {
						t.Errorf("warning %+v; want one under cloudwatch:%s naming %s", x, d.op, failing)
					}
				}
			})
		}
	}
}

// With more metric streams than the fan-out bound and one hard
// GetMetricStream failure, every other stream is still read and stored with
// its body: one failure cancels no sibling.
func TestScanCloudWatch_OneDetailFailureCancelsNoSibling(t *testing.T) {
	st := newTestStore(t)
	calls := newCWCalls()
	handlers := cwHandlers(calls)
	k := cwKindOf(t, TypeCloudWatchMetricStream)
	var names []string
	for i := range fanoutMed * 2 {
		names = append(names, k.name(fmt.Sprintf("s%d", i)))
	}
	handlers["ListMetricStreams"] = func(any) (any, error) {
		var elems []any
		for _, name := range names {
			elems = append(elems, k.elem(k.arnPrefix+name, name, false))
		}
		return cwListOutput(k.op, elems, nil), nil
	}
	handlers["GetMetricStream"] = func(in any) (any, error) {
		if sv(in.(*cloudwatch.GetMetricStreamInput).Name) == names[0] {
			return nil, apiErr("InvalidParameterCombination", "bad")
		}
		return calls.getMetricStream(in)
	}

	_, _, err := cwScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterCombination")
	if errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want no cancellation", err)
	}
	rows := rowsByNativeID(t, st, TypeCloudWatchMetricStream)
	if len(rows) != len(names) {
		t.Fatalf("metric stream rows = %d; want %d", len(rows), len(names))
	}
	for _, name := range names[1:] {
		arn := k.arnPrefix + name
		assertJSONEqual(t, arn+" attrs", rows[arn].AttributesJSON, mustJSON(cwStreamBody(name)))
	}
}
