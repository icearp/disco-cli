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
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rtbfabric"
	rtbtypes "github.com/aws/aws-sdk-go-v2/service/rtbfabric/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// rtbTestTotal is every row of the fixture: two requester and two
	// responder gateways, two links under each gateway and two routing
	// rules under each link. Every listing returns one element per page.
	rtbTestTotal = 4 + 2*4 + 2*8
)

var (
	rtbTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: rtbfabric:List")
	rtbTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	rtbTestBad      = apiErr("ValidationException", "bad")
	rtbTestGone     = apiErr("ResourceNotFoundException", "gone")
	// rtbTestTime is every listed link's and rule's CreatedAt and UpdatedAt,
	// which the rows do not carry.
	rtbTestTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	// rtbTestRequesters and rtbTestResponders are the gateway ids each
	// gateway listing returns, one per page, in order.
	rtbTestRequesters = []string{"rtb-gw-req-tagged", "rtb-gw-req-bare"}
	rtbTestResponders = []string{"rtb-gw-resp-tagged", "rtb-gw-resp-bare"}
)

func rtbTestGatewayARN(id string) string {
	return "arn:aws:rtbfabric:" + testRegion + ":" + testAccountID + ":gateway/" + id
}

// rtbTestGateways is every gateway id of the fixture.
func rtbTestGateways() []string {
	return append(slices.Clone(rtbTestRequesters), rtbTestResponders...)
}

// rtbTestLinkARNs is the ARN of every link of the fixture.
func rtbTestLinkARNs() []string {
	var arns []string
	for _, g := range rtbTestGateways() {
		arns = append(arns, rtbTestGatewayARN(g)+"/link/link-tagged", rtbTestGatewayARN(g)+"/link/link-bare")
	}
	return arns
}

// rtbTestRuleARNs is the ARN of every routing rule of the fixture.
func rtbTestRuleARNs() []string {
	var arns []string
	for _, l := range rtbTestLinkARNs() {
		arns = append(arns, l+"/routing-rule/rule-tagged", l+"/routing-rule/rule-bare")
	}
	return arns
}

// rtbTestPath is the ids a call's input names, joined by "/": its GatewayId
// and LinkId, each when set.
func rtbTestPath(in any) string {
	var parts []string
	for _, f := range []string{"GatewayId", "LinkId"} {
		if n := bacField[*string](in, f); n != nil {
			parts = append(parts, *n)
		}
	}
	return strings.Join(parts, "/")
}

// rtbCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type rtbCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	paths      map[string][]string
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newRTBCalls tags every "-tagged" gateway and rule with its type.
func newRTBCalls() *rtbCalls {
	c := &rtbCalls{maxResults: map[string][]int32{}, paths: map[string][]string{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, g := range rtbTestRequesters[:1] {
		c.tags[rtbTestGatewayARN(g)] = map[string]string{"type": TypeRTBFabricRequesterGateway}
	}
	for _, g := range rtbTestResponders[:1] {
		c.tags[rtbTestGatewayARN(g)] = map[string]string{"type": TypeRTBFabricResponderGateway}
	}
	for _, r := range rtbTestRuleARNs() {
		if strings.HasSuffix(r, "-tagged") {
			c.tags[r] = map[string]string{"type": TypeRTBFabricLinkRoutingRule}
		}
	}
	return c
}

// recordRTB notes op's MaxResults (0 when unset) and, on a first page, the
// path its input names.
func (c *rtbCalls) recordRTB(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := bacField[*int32](in, "MaxResults")
	if n == nil {
		n = sdkaws.Int32(0)
	}
	c.maxResults[op] = append(c.maxResults[op], *n)
	if bacField[*string](in, "NextToken") == nil {
		c.paths[op] = append(c.paths[op], rtbTestPath(in))
	}
}

func (c *rtbCalls) calledRTB(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

func (c *rtbCalls) maxResultsRTB(op string) []int32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.maxResults[op])
}

// pathsRTB is the paths op was called for, sorted.
func (c *rtbCalls) pathsRTB(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.paths[op]))
}

func (c *rtbCalls) tagARNsRTB() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *rtbCalls) listTagsRTB(in any) (any, error) {
	arn := sv(in.(*rtbfabric.ListTagsForResourceInput).ResourceArn)
	c.recordRTB("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &rtbfabric.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// rtbPage is a two-page listing: the first page (no NextToken sent) holds
// the "tagged" element and points at the second, which holds the "bare" one.
func rtbPage(in any) (suffix string, next *string) {
	if bacField[*string](in, "NextToken") != nil {
		return "bare", nil
	}
	return "tagged", sdkaws.String("p2")
}

// rtbHandlers answers every op the scan calls: each listing in two pages,
// the tag reads from c. The "tagged" link carries its tags in the listed
// element. The "bare" link and rule have no Status, and the "bare" link
// names the listed gateway as its peer, as a link seen from its other side
// would.
func rtbHandlers(c *rtbCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTagsRTB,
		"ListRequesterGateways": func(in any) (any, error) {
			c.recordRTB("ListRequesterGateways", in)
			suffix, next := rtbPage(in)
			return &rtbfabric.ListRequesterGatewaysOutput{GatewayIds: []string{"rtb-gw-req-" + suffix}, NextToken: next}, nil
		},
		"ListResponderGateways": func(in any) (any, error) {
			c.recordRTB("ListResponderGateways", in)
			suffix, next := rtbPage(in)
			return &rtbfabric.ListResponderGatewaysOutput{GatewayIds: []string{"rtb-gw-resp-" + suffix}, NextToken: next}, nil
		},
		"ListLinks": func(in any) (any, error) {
			c.recordRTB("ListLinks", in)
			suffix, next := rtbPage(in)
			l := rtbtypes.ListLinksResponseStructure{
				LinkId: sdkaws.String("link-" + suffix), GatewayId: bacField[*string](in, "GatewayId"), PeerGatewayId: sdkaws.String("rtb-gw-peer"),
				Status: rtbtypes.LinkStatusActive, CreatedAt: sdkaws.Time(rtbTestTime), UpdatedAt: sdkaws.Time(rtbTestTime),
			}
			if suffix == "tagged" {
				l.Tags = map[string]string{"type": TypeRTBFabricLink}
			} else {
				l.GatewayId, l.PeerGatewayId, l.Status = l.PeerGatewayId, l.GatewayId, ""
			}
			return &rtbfabric.ListLinksOutput{Links: []rtbtypes.ListLinksResponseStructure{l}, NextToken: next}, nil
		},
		"ListLinkRoutingRules": func(in any) (any, error) {
			c.recordRTB("ListLinkRoutingRules", in)
			suffix, next := rtbPage(in)
			r := rtbtypes.LinkRoutingRuleSummary{
				RuleId: sdkaws.String("rule-" + suffix), Priority: sdkaws.Int32(7), Status: rtbtypes.RuleStatusActive,
				Conditions: &rtbtypes.RuleCondition{HostHeader: sdkaws.String("x.example.com")}, CreatedAt: sdkaws.Time(rtbTestTime), UpdatedAt: sdkaws.Time(rtbTestTime),
			}
			if suffix == "bare" {
				r.Status = ""
			}
			return &rtbfabric.ListLinkRoutingRulesOutput{Rules: []rtbtypes.LinkRoutingRuleSummary{r}, NextToken: next}, nil
		},
	}
}

// rtbScanIn runs the scan's real entry point for acct in region against
// handlers; every op they do not answer is denied.
func rtbScanIn(ctx context.Context, st *store.Store, acct *account, region string, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct.cfg = opStubConfig(handlers, rtbTestDenied)
	return scanRTBFabric(ctx, acct, region, st, testScanID)
}

func rtbScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return rtbScanIn(ctx, st, newTestAccount(testAccountID), testRegion, handlers)
}

// rtbTaggedARNs is the ARN of every row whose tags are read (the gateways
// and the routing rules), sorted.
func rtbTaggedARNs() []string {
	var arns []string
	for _, g := range rtbTestGateways() {
		arns = append(arns, rtbTestGatewayARN(g))
	}
	arns = append(arns, rtbTestRuleARNs()...)
	slices.Sort(arns)
	return arns
}

// rtbWarnOps is the op of each warning, in order.
func rtbWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// failRTB makes op fail with err for the calls whose input names path,
// recording them; the others are answered as before.
func failRTB(calls *rtbCalls, handlers map[string]func(in any) (any, error), op, path string, err error) {
	answer := handlers[op]
	handlers[op] = func(in any) (any, error) {
		if rtbTestPath(in) == path {
			calls.recordRTB(op, in)
			return nil, err
		}
		return answer(in)
	}
}

// assertRTBTags fails unless r's TagsJSON is want, nil for none.
func assertRTBTags(t *testing.T, arn string, r store.Resource, want map[string]string) {
	t.Helper()
	if want != nil {
		assertTagsMap(t, r.TagsJSON, want)
		return
	}
	if r.TagsJSON != nil {
		t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
	}
}

// Every listing stores both its pages, for every parent. Every row carries
// the account's name. A gateway is stored under gateway/{id}, named by its
// id, its attributes its id, no Status; a link under the ARN of the gateway
// it was listed under (not the element's GatewayId) + /link/{LinkId}, a rule
// under its link's ARN + /routing-rule/{RuleId}, each named by its id, its
// Status the listed one (nil when the element has none), its attributes the
// listed element, no CreatedAt. The gateways and
// rules have the tags their tag read returns, the links the tags their
// element carries (nil for none). ListTagsForResource is sent exactly the
// gateway and rule ARNs, never a link's. Links are listed for every
// gateway, rules for every link; the gateway and rule listings send the
// model's MaxResults cap of 10, ListLinks (no modeled range) none. No error,
// no warning.
func TestScanRTBFabric_StoresEveryRowWithItsTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRTBCalls()

	total, inserted, err := rtbScan(context.Background(), st, rtbHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != rtbTestTotal || inserted != rtbTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, rtbTestTotal, rtbTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for typ, ids := range map[string][]string{TypeRTBFabricRequesterGateway: rtbTestRequesters, TypeRTBFabricResponderGateway: rtbTestResponders} {
		rows := rowsByNativeID(t, st, typ)
		if len(rows) != len(ids) {
			t.Errorf("%s rows = %d; want %d", typ, len(rows), len(ids))
		}
		for _, id := range ids {
			arn := rtbTestGatewayARN(id)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", typ, arn)
				continue
			}
			if sv(r.AccountName) != "Test Account" {
				t.Errorf("%s AccountName = %q; want Test Account", arn, sv(r.AccountName))
			}
			if sv(r.Name) != id || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil || r.AttributesJSON != `{"GatewayId":"`+id+`"}` {
				t.Errorf("%s Name, Region, Status, CreatedAt, attrs = %q, %q, %v, %v, %s; want %q, %q, no Status, no CreatedAt, its id", arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.AttributesJSON, id, testRegion)
			}
			var want map[string]string
			if strings.HasSuffix(id, "-tagged") {
				want = map[string]string{"type": typ}
			}
			assertRTBTags(t, arn, r, want)
		}
	}
	for typ, arns := range map[string][]string{TypeRTBFabricLink: rtbTestLinkARNs(), TypeRTBFabricLinkRoutingRule: rtbTestRuleARNs()} {
		rows := rowsByNativeID(t, st, typ)
		if len(rows) != len(arns) {
			t.Errorf("%s rows = %d; want %d", typ, len(rows), len(arns))
		}
		for _, arn := range arns {
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", typ, arn)
				continue
			}
			id := arn[strings.LastIndex(arn, "/")+1:]
			if sv(r.AccountName) != "Test Account" {
				t.Errorf("%s AccountName = %q; want Test Account", arn, sv(r.AccountName))
			}
			if sv(r.Name) != id || sv(r.Region) != testRegion || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, CreatedAt = %q, %q, %v; want %q, %q, nil", arn, sv(r.Name), sv(r.Region), r.CreatedAt, id, testRegion)
			}
			if strings.HasSuffix(id, "-bare") {
				if r.Status != nil {
					t.Errorf("%s Status = %q; want nil", arn, *r.Status)
				}
			} else if sv(r.Status) != "ACTIVE" {
				t.Errorf("%s Status = %q; want ACTIVE", arn, sv(r.Status))
			}
			var attrs struct {
				LinkID   string `json:"LinkId"`
				RuleID   string `json:"RuleId"`
				Priority int32
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || (typ == TypeRTBFabricLink && attrs.LinkID != id) || (typ == TypeRTBFabricLinkRoutingRule && (attrs.RuleID != id || attrs.Priority != 7)) {
				t.Errorf("%s attrs = %s (%v); want the listed element", arn, r.AttributesJSON, err)
			}
			var want map[string]string
			if strings.HasSuffix(arn, "-tagged") {
				want = map[string]string{"type": typ}
			}
			assertRTBTags(t, arn, r, want)
		}
	}
	if got := calls.tagARNsRTB(); !slices.Equal(got, rtbTaggedARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, rtbTaggedARNs())
	}
	var perLink []string
	for _, g := range rtbTestGateways() {
		perLink = append(perLink, g+"/link-bare", g+"/link-tagged")
	}
	for op, want := range map[string][]string{
		"ListLinks":            slices.Sorted(slices.Values(rtbTestGateways())),
		"ListLinkRoutingRules": slices.Sorted(slices.Values(perLink)),
	} {
		if got := calls.pathsRTB(op); !slices.Equal(got, want) {
			t.Errorf("%s paths = %v; want %v", op, got, want)
		}
	}
	for op, want := range map[string]int32{"ListRequesterGateways": 10, "ListResponderGateways": 10, "ListLinkRoutingRules": 10, "ListLinks": 0} {
		got := calls.maxResultsRTB(op)
		if len(got) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range got {
			if n != want {
				t.Errorf("%s MaxResults = %d; want %d", op, n, want)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanRTBFabric_RescanInsertsNone(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := rtbScan(context.Background(), st, rtbHandlers(newRTBCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := rtbScan(context.Background(), st, rtbHandlers(newRTBCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != rtbTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, rtbTestTotal)
	}
}

// No gateway listed: nothing stored, no child call or tag read, no error.
// Empty listings warn nothing; denied ones warn once each under their op.
func TestScanRTBFabric_NoGateways(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		err       error
		wantWarns []string
	}{
		{"empty", nil, nil},
		{"denied", rtbTestDenied, []string{"rtbfabric:ListRequesterGateways", "rtbfabric:ListResponderGateways"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRTBCalls()
			handlers := rtbHandlers(calls)
			handlers["ListRequesterGateways"] = func(in any) (any, error) {
				calls.recordRTB("ListRequesterGateways", in)
				if c.err != nil {
					return nil, c.err
				}
				return &rtbfabric.ListRequesterGatewaysOutput{}, nil
			}
			handlers["ListResponderGateways"] = func(in any) (any, error) {
				calls.recordRTB("ListResponderGateways", in)
				if c.err != nil {
					return nil, c.err
				}
				return &rtbfabric.ListResponderGatewaysOutput{}, nil
			}

			total, inserted, err := rtbScan(context.Background(), st, handlers)
			if err != nil || total != 0 || inserted != 0 {
				t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
			}
			if got := rtbWarnOps(warns()); !slices.Equal(got, c.wantWarns) {
				t.Errorf("warnings = %v; want %v", got, c.wantWarns)
			}
			for _, op := range []string{"ListLinks", "ListLinkRoutingRules", "ListTagsForResource"} {
				if n := calls.calledRTB(op); n != 0 {
					t.Errorf("%s called %d times; want 0", op, n)
				}
			}
		})
	}
}

// A failed tag read of a gateway or rule: the modeled
// ResourceNotFoundException drops the row (the resource was deleted after
// it was listed) without a warning or an error, and a dropped gateway has
// no links listed. Any other failure keeps the row untagged: a denial warns
// once under the tag op with the scan's scope, naming the row; a throttle
// is transient; any other error is an error.
func TestScanRTBFabric_TagReadFailures(t *testing.T) {
	t.Parallel()
	gateway := rtbTestGatewayARN("rtb-gw-resp-tagged")
	rule := rtbTestRuleARNs()[0]
	for _, c := range []struct {
		name     string
		arn, typ string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		warn     bool
		// dropped: the row is not stored; lost: the rows not stored, its
		// descendants included.
		dropped bool
		lost    int
	}{
		{"gateway/denied", gateway, TypeRTBFabricResponderGateway, rtbTestDenied, -1, "", true, false, 0},
		{"gateway/transient", gateway, TypeRTBFabricResponderGateway, rtbTestThrottle, outcomeTransient, "ThrottlingException", false, false, 0},
		{"gateway/other error", gateway, TypeRTBFabricResponderGateway, rtbTestBad, outcomeError, "ValidationException", false, false, 0},
		// The gateway, its two links and their four rules.
		{"gateway/not found", gateway, TypeRTBFabricResponderGateway, rtbTestGone, -1, "", false, true, 1 + 2 + 4},
		{"rule/denied", rule, TypeRTBFabricLinkRoutingRule, rtbTestDenied, -1, "", true, false, 0},
		{"rule/transient", rule, TypeRTBFabricLinkRoutingRule, rtbTestThrottle, outcomeTransient, "ThrottlingException", false, false, 0},
		{"rule/other error", rule, TypeRTBFabricLinkRoutingRule, rtbTestBad, outcomeError, "ValidationException", false, false, 0},
		{"rule/not found", rule, TypeRTBFabricLinkRoutingRule, rtbTestGone, -1, "", false, true, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRTBCalls()
			calls.errs[c.arn] = c.err

			total, _, err := rtbScan(context.Background(), st, rtbHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if want := rtbTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			switch {
			case c.dropped && ok:
				t.Error("row stored; want it dropped")
			case !c.dropped && !ok:
				t.Fatal("row not stored; want it kept")
			case ok && r.TagsJSON != nil:
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if c.dropped && c.typ == TypeRTBFabricResponderGateway && slices.Contains(calls.pathsRTB("ListLinks"), "rtb-gw-resp-tagged") {
				t.Error("the dropped gateway had its links listed")
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{rtbTagsOp}
			}
			if got := rtbWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", x.Scope, x.Message, testAccountID, testRegion, c.arn)
				}
			}
		})
	}
}

// Denied tag reads in the gateway and rule phases send one warning for the
// whole scan; the rows are kept.
func TestScanRTBFabric_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRTBCalls()
	for _, arn := range rtbTaggedARNs() {
		calls.errs[arn] = rtbTestDenied
	}

	total, _, err := rtbScan(context.Background(), st, rtbHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != rtbTestTotal {
		t.Errorf("total = %d; want %d", total, rtbTestTotal)
	}
	if got := rtbWarnOps(warns()); !slices.Equal(got, []string{rtbTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, rtbTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanRTBFabric_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, rtbTagsOp)
	calls := newRTBCalls()
	calls.errs[rtbTestGatewayARN("rtb-gw-req-bare")] = rtbTestThrottle
	calls.errs[rtbTestRuleARNs()[5]] = rtbTestBad

	_, _, err := rtbScan(context.Background(), st, rtbHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", rtbTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanRTBFabric_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeRTBFabricResponderGateway)
	calls := newRTBCalls()
	calls.errs[rtbTestGatewayARN("rtb-gw-resp-tagged")] = rtbTestDenied

	total, _, err := rtbScan(context.Background(), st, rtbHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListLinks", "ListLinkRoutingRules"} {
		if n := calls.calledRTB(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeRTBFabricRequesterGateway)); total != 2 || n != 2 {
		t.Errorf("total = %d, requester gateways stored = %d; want 2, 2", total, n)
	}
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases. A parent not stored has no children
// read.
func TestScanRTBFabric_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	reqLink := rtbTestGatewayARN("rtb-gw-req-tagged") + "/link/"
	for _, c := range []struct {
		op, path, typ string
		kept, lost    string
		// wantTotal is the rows stored; wantRules those of the last phase.
		wantTotal, wantRules int
	}{
		// The second requester page: rtb-gw-req-bare, its 2 links and their
		// 4 rules are lost.
		{"ListRequesterGateways", "", TypeRTBFabricRequesterGateway, rtbTestGatewayARN("rtb-gw-req-tagged"), rtbTestGatewayARN("rtb-gw-req-bare"), rtbTestTotal - 7, 12},
		// One gateway's second page of links: link-bare and its 2 rules
		// are lost.
		{"ListLinks", "rtb-gw-req-tagged", TypeRTBFabricLink, reqLink + "link-tagged", reqLink + "link-bare", rtbTestTotal - 3, 14},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newRTBCalls()
			handlers := rtbHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil && rtbTestPath(in) == c.path {
					calls.recordRTB(c.op, in)
					return nil, rtbTestBad
				}
				return list(in)
			}

			total, _, err := rtbScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := len(rowsByNativeID(t, st, TypeRTBFabricLinkRoutingRule)); got != c.wantRules {
				t.Errorf("routing rules (the last phase) = %d; want %d", got, c.wantRules)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanRTBFabric_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newRTBCalls()
	handlers := rtbHandlers(calls)
	twoPages := handlers["ListLinkRoutingRules"]
	handlers["ListLinkRoutingRules"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*rtbfabric.ListLinkRoutingRulesOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := rtbScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != rtbTestTotal {
		t.Errorf("total = %d; want %d", total, rtbTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeRTBFabricLinkRoutingRule)); n != 16 {
		t.Errorf("routing rules = %d; want 16", n)
	}
}

// One parent's failed child listing costs the other parents none of their
// rows: a parent deleted since it was listed (the modeled
// ResourceNotFoundException) is skipped silently, a denial warns once under
// the op, and any other error is returned.
func TestScanRTBFabric_PerParentFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op, path string
		// lost is the rows the failed parent's listing would have stored,
		// with their descendants.
		lost int
	}{
		{"ListLinks", "rtb-gw-resp-bare", 2 + 4},
		{"ListLinkRoutingRules", "rtb-gw-req-tagged/link-bare", 2},
	} {
		for _, e := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"parent gone", rtbTestGone, "", 0},
			{"denied", rtbTestDenied, "", 1},
			{"other error", rtbTestBad, "ValidationException", 0},
		} {
			t.Run(c.op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newRTBCalls()
				handlers := rtbHandlers(calls)
				failRTB(calls, handlers, c.op, c.path, e.err)

				total, _, err := rtbScan(context.Background(), st, handlers)
				if e.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, e.code)
				}
				if want := rtbTestTotal - c.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				w := warns()
				if len(w) != e.wantWarns || (len(w) == 1 && w[0].Service != "rtbfabric:"+c.op) {
					t.Errorf("warnings = %v; want %d under rtbfabric:%s", w, e.wantWarns, c.op)
				}
			})
		}
	}
}

// A gateway with an empty id, a link without a LinkId or a rule without a
// RuleId is not stored, is not sent to the tag op, has no children read,
// and is warned about once under its listing, naming it.
func TestScanRTBFabric_ElementWithoutID(t *testing.T) {
	t.Parallel()
	link := rtbTestGatewayARN("rtb-gw-req-tagged") + "/link/link-tagged"
	for _, c := range []struct {
		name, op, key, typ string
		answer             func(in any) any
		wantTotal          int
		// childOp is the op the element's children would be read by; ""
		// for none.
		childOp string
	}{
		{
			"gateway with an empty id", "ListRequesterGateways", noIdentifier, TypeRTBFabricRequesterGateway,
			func(any) any { return &rtbfabric.ListRequesterGatewaysOutput{GatewayIds: []string{""}} },
			// The responder gateways and their descendants.
			2 + 4 + 8, "ListLinks",
		},
		{
			"link without a LinkId", "ListLinks", "rtb-gw-req-tagged", TypeRTBFabricLink,
			func(in any) any {
				if rtbTestPath(in) != "rtb-gw-req-tagged" {
					return &rtbfabric.ListLinksOutput{}
				}
				return &rtbfabric.ListLinksOutput{Links: []rtbtypes.ListLinksResponseStructure{{GatewayId: sdkaws.String("rtb-gw-req-tagged"), Status: rtbtypes.LinkStatusActive}}}
			},
			// Only the gateways are stored.
			4, "ListLinkRoutingRules",
		},
		{
			"rule without a RuleId", "ListLinkRoutingRules", link, TypeRTBFabricLinkRoutingRule,
			func(in any) any {
				if rtbTestPath(in) != "rtb-gw-req-tagged/link-tagged" {
					return &rtbfabric.ListLinkRoutingRulesOutput{}
				}
				return &rtbfabric.ListLinkRoutingRulesOutput{Rules: []rtbtypes.LinkRoutingRuleSummary{{Priority: sdkaws.Int32(1), Status: rtbtypes.RuleStatusActive}}}
			},
			// The gateways and the links.
			4 + 8, "",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRTBCalls()
			handlers := rtbHandlers(calls)
			handlers[c.op] = func(in any) (any, error) {
				calls.recordRTB(c.op, in)
				return c.answer(in), nil
			}

			total, _, err := rtbScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "rtbfabric:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under rtbfabric:%s naming %s", w, c.op, c.key)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 0 {
				t.Errorf("%s rows = %d; want 0", c.typ, n)
			}
			for _, arn := range calls.tagARNsRTB() {
				if strings.HasSuffix(arn, "/") {
					t.Errorf("tag read sent %s, an ARN with an empty id", arn)
				}
			}
			if c.childOp == "ListLinks" && slices.Contains(calls.pathsRTB("ListLinks"), "") {
				t.Error("links were listed for an empty gateway id")
			}
			if c.childOp == "ListLinkRoutingRules" && calls.calledRTB("ListLinkRoutingRules") != 0 {
				t.Errorf("ListLinkRoutingRules paths = %v; want none", calls.pathsRTB("ListLinkRoutingRules"))
			}
		})
	}
}

// In GovCloud and China every NativeID is built in the account's partition
// and region, and the gateway and rule ARNs reach the tag op unchanged.
func TestScanRTBFabric_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			gatewayARN := partitionARN(pf.partition, "rtbfabric", pf.region, "gateway/rtb-gw-resp-tagged")
			linkARN := gatewayARN + "/link/link-tagged"
			ruleARN := linkARN + "/routing-rule/rule-tagged"
			st := newTestStore(t)
			calls := newRTBCalls()
			calls.tags[gatewayARN] = map[string]string{"env": "prod"}
			calls.tags[ruleARN] = map[string]string{"env": "rule"}
			handlers := rtbHandlers(calls)
			handlers["ListRequesterGateways"] = func(in any) (any, error) {
				calls.recordRTB("ListRequesterGateways", in)
				return &rtbfabric.ListRequesterGatewaysOutput{}, nil
			}
			for _, op := range []string{"ListResponderGateways", "ListLinks", "ListLinkRoutingRules"} {
				handlers[op] = rtbFirstPageOnly(handlers[op])
			}

			if _, _, err := rtbScanIn(context.Background(), st, partitionAccount(pf.partition), pf.region, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := calls.tagARNsRTB(), []string{gatewayARN, ruleARN}; !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeRTBFabricResponderGateway)[gatewayARN].TagsJSON, map[string]string{"env": "prod"})
			assertTagsMap(t, rowsByNativeID(t, st, TypeRTBFabricLinkRoutingRule)[ruleARN].TagsJSON, map[string]string{"env": "rule"})
			if _, ok := rowsByNativeID(t, st, TypeRTBFabricLink)[linkARN]; !ok {
				t.Errorf("link %s not stored", linkARN)
			}
		})
	}
}

// rtbFirstPageOnly is the listing h with its first page's NextToken
// cleared, so the listing ends after that page.
func rtbFirstPageOnly(h func(in any) (any, error)) func(in any) (any, error) {
	return func(in any) (any, error) {
		out, err := h(in)
		switch o := out.(type) {
		case *rtbfabric.ListResponderGatewaysOutput:
			o.NextToken = nil
		case *rtbfabric.ListLinksOutput:
			o.NextToken = nil
		case *rtbfabric.ListLinkRoutingRulesOutput:
			o.NextToken = nil
		}
		return out, err
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanRTBFabric_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newRTBCalls()
	handlers := rtbHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListLinks"]
	handlers["ListLinks"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := rtbScan(ctx, st, handlers)
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
	if n := calls.calledRTB("ListLinkRoutingRules"); n != 0 {
		t.Errorf("ListLinkRoutingRules called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanRTBFabric_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "rtbfabric.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("last phase after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := rtbHandlers(newRTBCalls())
		handlers["ListLinkRoutingRules"] = func(any) (any, error) { return nil, nx }
		total, _, err := rtbScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := rtbTestTotal - 16; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newRTBCalls()
		calls.errs[rtbTestGatewayARN("rtb-gw-req-bare")] = nx
		_, _, err := rtbScan(context.Background(), st, rtbHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanRTBFabric(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
