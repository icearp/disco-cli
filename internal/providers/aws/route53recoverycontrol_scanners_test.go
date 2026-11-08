package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/route53recoverycontrolconfig"
	r53rctypes "github.com/aws/aws-sdk-go-v2/service/route53recoverycontrolconfig/types"
	"github.com/icearp/disco-cli/store"
)

// r53rcTestTotal is every row of the fixture: two clusters, two control
// panels, and two routing controls and two safety rules under each panel.
// Every listing returns one element per page, "tagged" on the first and
// "bare" on the second.
const r53rcTestTotal = 2 + 2 + 2*2 + 2*2

// r53rcTestHome is the region every call must reach: the service's home for
// aws accounts.
const r53rcTestHome = "us-west-2"

var (
	r53rcTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: route53-recovery-control-config:List")
	r53rcTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	r53rcTestBad      = apiErr("ValidationException", "bad")
	r53rcTestGone     = apiErr("ResourceNotFoundException", "gone")
)

func r53rcTestARNIn(account, resource string) string {
	return "arn:aws:route53-recovery-control::" + account + ":" + resource
}

func r53rcClusterARN(suffix string) string { return r53rcTestARNIn(testAccountID, "cluster/c-"+suffix) }

func r53rcPanelARN(suffix string) string {
	return r53rcTestARNIn(testAccountID, "controlpanel/p-"+suffix)
}

// r53rcTestParent is the control panel ARN a child listing's input sends; ""
// for a top-level listing.
func r53rcTestParent(in any) string {
	if p := bacField[*string](in, "ControlPanelArn"); p != nil {
		return *p
	}
	return ""
}

// r53rcAssertion reports whether the safety rule suffix lists under panel is
// an assertion rule rather than a gating rule: under panel p-tagged the
// first page's rule asserts, under p-bare the second page's, so each arm is
// listed both tagged and bare.
func r53rcAssertion(panel, suffix string) bool {
	return strings.HasSuffix(panel, "tagged") == (suffix == "tagged")
}

// r53rcKind is one listing: its op, the type it stores, whether it lists per
// control panel and whether its rows have their tags read; for the element
// suffix lists under panel ("" for a top-level listing): native, the row's
// NativeID; name, its Name; listedARN, the ARN its stored attributes carry;
// page, the op's output holding that one element and next.
type r53rcKind struct {
	op, typ   string
	perPanel  bool
	tagged    bool
	native    func(panel, suffix string) string
	name      func(suffix string) string
	listedARN func(attrs map[string]any) any
	page      func(panel, suffix string, next *string) any
}

// r53rcTestStatus is the status of every element suffix lists: DEPLOYED for
// "tagged", PENDING for "bare". Under each panel the safety rules' arms swap
// (r53rcAssertion), so each arm is listed with both statuses, and each child's
// status differs from its panel's under one of the two panels.
func r53rcTestStatus(suffix string) r53rctypes.Status {
	if suffix == "tagged" {
		return r53rctypes.StatusDeployed
	}
	return r53rctypes.StatusPending
}

func r53rcKinds() []r53rcKind {
	field := func(f string) func(map[string]any) any { return func(a map[string]any) any { return a[f] } }
	rc := func(p, s string) string { return p + "/routingcontrol/rc-" + s }
	sr := func(p, s string) string { return p + "/safetyrule/sr-" + s }
	return []r53rcKind{
		{
			op: "ListClusters", typ: TypeR53RCCluster, tagged: true,
			native:    func(_, s string) string { return r53rcClusterARN(s) },
			name:      func(s string) string { return "c-" + s },
			listedARN: field("ClusterArn"),
			page: func(_, s string, next *string) any {
				return &route53recoverycontrolconfig.ListClustersOutput{NextToken: next, Clusters: []r53rctypes.Cluster{{
					ClusterArn: sdkaws.String(r53rcClusterARN(s)), Name: sdkaws.String("c-" + s), Status: r53rcTestStatus(s), Owner: sdkaws.String(testAccountID),
				}}}
			},
		},
		{
			op: "ListControlPanels", typ: TypeR53RCControlPanel, tagged: true,
			native:    func(_, s string) string { return r53rcPanelARN(s) },
			name:      func(s string) string { return "p-" + s },
			listedARN: field("ControlPanelArn"),
			page: func(_, s string, next *string) any {
				// The tagged panel is its cluster's default one: it is stored
				// and tag-read like any other.
				return &route53recoverycontrolconfig.ListControlPanelsOutput{NextToken: next, ControlPanels: []r53rctypes.ControlPanel{{
					ControlPanelArn: sdkaws.String(r53rcPanelARN(s)), ClusterArn: sdkaws.String(r53rcClusterARN(s)), Name: sdkaws.String("p-" + s),
					Status: r53rcTestStatus(s), DefaultControlPanel: sdkaws.Bool(s == "tagged"), Owner: sdkaws.String(testAccountID),
				}}}
			},
		},
		{
			op: "ListRoutingControls", typ: TypeR53RCRoutingControl, perPanel: true,
			native:    rc,
			name:      func(s string) string { return "rc-" + s },
			listedARN: field("RoutingControlArn"),
			page: func(p, s string, next *string) any {
				return &route53recoverycontrolconfig.ListRoutingControlsOutput{NextToken: next, RoutingControls: []r53rctypes.RoutingControl{{
					RoutingControlArn: sdkaws.String(rc(p, s)), ControlPanelArn: sdkaws.String(p), Name: sdkaws.String("rc-" + s), Status: r53rcTestStatus(s),
				}}}
			},
		},
		{
			op: "ListSafetyRules", typ: TypeR53RCSafetyRule, perPanel: true, tagged: true,
			native: sr,
			name:   func(s string) string { return "sr-" + s },
			listedARN: func(a map[string]any) any {
				for _, arm := range []string{"ASSERTION", "GATING"} {
					if m, ok := a[arm].(map[string]any); ok {
						return arm + " " + m["SafetyRuleArn"].(string)
					}
				}
				return nil
			},
			page: func(p, s string, next *string) any {
				rule := r53rctypes.Rule{}
				if r53rcAssertion(p, s) {
					rule.ASSERTION = &r53rctypes.AssertionRule{SafetyRuleArn: sdkaws.String(sr(p, s)), ControlPanelArn: sdkaws.String(p), Name: sdkaws.String("sr-" + s), Status: r53rcTestStatus(s)}
				} else {
					rule.GATING = &r53rctypes.GatingRule{SafetyRuleArn: sdkaws.String(sr(p, s)), ControlPanelArn: sdkaws.String(p), Name: sdkaws.String("sr-" + s), Status: r53rcTestStatus(s)}
				}
				return &route53recoverycontrolconfig.ListSafetyRulesOutput{NextToken: next, SafetyRules: []r53rctypes.Rule{rule}}
			},
		},
	}
}

func r53rcKindOf(t *testing.T, op string) r53rcKind {
	t.Helper()
	for _, k := range r53rcKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no Route53 Recovery Control listing", op)
	return r53rcKind{}
}

// r53rcListed is one element of the fixture: its panel's ARN and suffix.
type r53rcListed struct{ panel, suffix string }

func (k r53rcKind) listedR53RC() []r53rcListed {
	panels := []string{""}
	if k.perPanel {
		panels = []string{r53rcPanelARN("tagged"), r53rcPanelARN("bare")}
	}
	var out []r53rcListed
	for _, p := range panels {
		out = append(out, r53rcListed{p, "tagged"}, r53rcListed{p, "bare"})
	}
	return out
}

// r53rcCalls records the calls of a scan, and the region each reached, and
// answers the tag reads, by ARN, with tags or errs.
type r53rcCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	parents    map[string][]string
	regions    map[string]bool
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newR53RCCalls tags every first-page row of a tagged kind with its type.
func newR53RCCalls() *r53rcCalls {
	c := &r53rcCalls{maxResults: map[string][]int32{}, parents: map[string][]string{}, regions: map[string]bool{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range r53rcKinds() {
		if !k.tagged {
			continue
		}
		for _, l := range k.listedR53RC() {
			if l.suffix == "tagged" {
				c.tags[k.native(l.panel, l.suffix)] = map[string]string{"type": k.typ}
			}
		}
	}
	return c
}

// recordR53RC notes op's MaxResults (0 when unset) and, on a first page, the
// panel its input names.
func (c *r53rcCalls) recordR53RC(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := bacField[*int32](in, "MaxResults")
	if n == nil {
		n = sdkaws.Int32(0)
	}
	c.maxResults[op] = append(c.maxResults[op], *n)
	if bacField[*string](in, "NextToken") == nil {
		c.parents[op] = append(c.parents[op], r53rcTestParent(in))
	}
}

func (c *r53rcCalls) calledR53RC(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

func (c *r53rcCalls) parentsR53RC(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.parents[op]))
}

func (c *r53rcCalls) tagARNsR53RC() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *r53rcCalls) listTagsR53RC(in any) (any, error) {
	arn := sv(in.(*route53recoverycontrolconfig.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &route53recoverycontrolconfig.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// twoPagesR53RC answers k's op with "tagged" on the first page and "bare" on
// the second, under the panel its input names.
func (c *r53rcCalls) twoPagesR53RC(k r53rcKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.recordR53RC(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(r53rcTestParent(in), suffix, next), nil
	}
}

func r53rcHandlers(c *r53rcCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTagsR53RC}
	for _, k := range r53rcKinds() {
		h[k.op] = c.twoPagesR53RC(k)
	}
	return h
}

// r53rcScanIn runs the scan's real entry point for acct against handlers,
// noting in calls the region every call reached; every op they do not answer
// is denied.
func r53rcScanIn(ctx context.Context, st *store.Store, acct *account, calls *r53rcCalls, handlers map[string]func(in any) (any, error)) (int, error) {
	withRegion := make(map[string]func(context.Context, any) (any, error), len(handlers))
	for op, h := range handlers {
		withRegion[op] = func(ctx context.Context, in any) (any, error) {
			calls.mu.Lock()
			calls.regions[awsmw.GetRegion(ctx)] = true
			calls.mu.Unlock()
			return h(in)
		}
	}
	acct.cfg = opStubConfigCtx(withRegion, r53rcTestDenied)
	return scanR53RecoveryControl(ctx, acct, "", st, testScanID)
}

func r53rcScan(ctx context.Context, st *store.Store, calls *r53rcCalls, handlers map[string]func(in any) (any, error)) (int, error) {
	return r53rcScanIn(ctx, st, newTestAccount(testAccountID), calls, handlers)
}

func r53rcWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, the per-panel listings for every
// panel stored this scan, each row under its listed ARN with its Name,
// Status and global Region, no CreatedAt, its attributes the listed element
// (a safety rule's from either arm), and, for clusters, control panels (the
// default one included) and safety rules, the tags its tag read returns (nil
// for none). Routing controls have no tags read. ListTagsForResource is sent
// exactly the listed ARNs of the tagged kinds; every listing is sent the
// model's MaxResults cap of 1000; every call, the tag reads included, reaches
// the home region. No error, no warning.
func TestScanR53RecoveryControl_StoresEveryRowWithItsTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newR53RCCalls()

	counted, upserts := countUpserts(st)
	total, err := r53rcScan(context.Background(), counted, calls, r53rcHandlers(calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != r53rcTestTotal || inserted != r53rcTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, r53rcTestTotal, r53rcTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	var wantTagARNs []string
	arms := map[string]int{}
	for _, k := range r53rcKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		listed := k.listedR53RC()
		if len(rows) != len(listed) {
			t.Errorf("%s rows = %d; want %d", k.typ, len(rows), len(listed))
		}
		for _, l := range listed {
			id := k.native(l.panel, l.suffix)
			r, ok := rows[id]
			if !ok {
				t.Errorf("%s %s missing", k.typ, id)
				continue
			}
			wantStatus := string(r53rcTestStatus(l.suffix))
			if sv(r.Name) != k.name(l.suffix) || sv(r.Region) != "global" || sv(r.Status) != wantStatus || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %q, %q, %q, %v, %v; want %q, global, %q, nil, false",
					id, sv(r.Name), sv(r.Region), sv(r.Status), r.CreatedAt, r.ManagedByProvider, k.name(l.suffix), wantStatus)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs: %v", id, err)
			}
			wantARN := any(id)
			if k.typ == TypeR53RCSafetyRule {
				arm := "GATING"
				if r53rcAssertion(l.panel, l.suffix) {
					arm = "ASSERTION"
				}
				arms[arm+" "+l.suffix]++
				wantARN = arm + " " + id
			}
			if got := k.listedARN(attrs); got != wantARN {
				t.Errorf("%s attrs carry %v; want %v (the listed element)", id, got, wantARN)
			}
			switch {
			case k.tagged && l.suffix == "tagged":
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			case r.TagsJSON != nil:
				t.Errorf("%s TagsJSON = %q; want nil", id, *r.TagsJSON)
			}
			if k.tagged {
				wantTagARNs = append(wantTagARNs, id)
			}
		}
	}
	if want := map[string]int{"ASSERTION tagged": 1, "ASSERTION bare": 1, "GATING tagged": 1, "GATING bare": 1}; !maps.Equal(arms, want) {
		t.Errorf("safety rule arms listed = %v; want %v", arms, want)
	}
	slices.Sort(wantTagARNs)
	if got := calls.tagARNsR53RC(); !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantTagARNs)
	}
	panels := []string{r53rcPanelARN("bare"), r53rcPanelARN("tagged")}
	for _, op := range []string{"ListRoutingControls", "ListSafetyRules"} {
		if got := calls.parentsR53RC(op); !slices.Equal(got, panels) {
			t.Errorf("%s panels = %v; want %v", op, got, panels)
		}
	}
	for _, k := range r53rcKinds() {
		if calls.calledR53RC(k.op) == 0 {
			t.Errorf("%s not called", k.op)
		}
		for _, n := range calls.maxResults[k.op] {
			if n != 1000 {
				t.Errorf("%s MaxResults = %d; want 1000", k.op, n)
			}
		}
	}
	if want := map[string]bool{r53rcTestHome: true}; !maps.Equal(calls.regions, want) {
		t.Errorf("regions called = %v; want only %s", calls.regions, r53rcTestHome)
	}
}

// A rescan reports every stored row in its total, not only the rows the store
// newly wrote. On a first scan into an empty store every row is new, so only a
// rescan catches a total taken from the store's written count.
func TestScanR53RecoveryControl_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	first := newR53RCCalls()
	if _, err := r53rcScan(context.Background(), st, first, r53rcHandlers(first)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	again := newR53RCCalls()
	total, err := r53rcScan(context.Background(), st, again, r53rcHandlers(again))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != r53rcTestTotal {
		t.Errorf("rescan total = %d; want %d", total, r53rcTestTotal)
	}
}

// Nothing listed: nothing stored, no per-panel call or tag read, no error,
// no warning.
func TestScanR53RecoveryControl_EmptyListings(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newR53RCCalls()
	handlers := r53rcHandlers(calls)
	handlers["ListClusters"] = func(any) (any, error) { return &route53recoverycontrolconfig.ListClustersOutput{}, nil }
	handlers["ListControlPanels"] = func(any) (any, error) { return &route53recoverycontrolconfig.ListControlPanelsOutput{}, nil }

	counted, upserts := countUpserts(st)
	total, err := r53rcScan(context.Background(), counted, calls, handlers)
	inserted := upserts()
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListRoutingControls", "ListSafetyRules"} {
		if n := calls.calledR53RC(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if got := calls.tagARNsR53RC(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
}

// A tag read that finds the resource gone (the modeled
// ResourceNotFoundException) drops its row silently, and a dropped panel has
// no children listed. Any other failure keeps the row untagged: a denial
// warns once under the tag op with the scan's scope; a throttle is
// transient; any other error is returned.
func TestScanR53RecoveryControl_TagReadFailures(t *testing.T) {
	t.Parallel()
	cluster := r53rcClusterARN("tagged")
	panel := r53rcPanelARN("tagged")
	// Under p-bare the first page's rule is a gating rule, the second's an
	// assertion rule.
	gating := r53rcKindOf(t, "ListSafetyRules").native(r53rcPanelARN("bare"), "tagged")
	assertion := r53rcKindOf(t, "ListSafetyRules").native(r53rcPanelARN("bare"), "bare")
	for _, c := range []struct {
		name     string
		arn, typ string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		warn     bool
		// lost is the rows not stored: the row dropped and its children.
		lost int
	}{
		{"cluster/denied", cluster, TypeR53RCCluster, r53rcTestDenied, -1, "", true, 0},
		{"cluster/transient", cluster, TypeR53RCCluster, r53rcTestThrottle, outcomeTransient, "ThrottlingException", false, 0},
		{"cluster/other error", cluster, TypeR53RCCluster, r53rcTestBad, outcomeError, "ValidationException", false, 0},
		{"cluster/gone", cluster, TypeR53RCCluster, r53rcTestGone, -1, "", false, 1},
		{"control panel/denied", panel, TypeR53RCControlPanel, r53rcTestDenied, -1, "", true, 0},
		{"control panel/other error", panel, TypeR53RCControlPanel, r53rcTestBad, outcomeError, "ValidationException", false, 0},
		{"control panel/gone", panel, TypeR53RCControlPanel, r53rcTestGone, -1, "", false, 1 + 2 + 2},
		{"gating rule/denied", gating, TypeR53RCSafetyRule, r53rcTestDenied, -1, "", true, 0},
		{"gating rule/transient", gating, TypeR53RCSafetyRule, r53rcTestThrottle, outcomeTransient, "ThrottlingException", false, 0},
		{"gating rule/gone", gating, TypeR53RCSafetyRule, r53rcTestGone, -1, "", false, 1},
		{"assertion rule/other error", assertion, TypeR53RCSafetyRule, r53rcTestBad, outcomeError, "ValidationException", false, 0},
		{"assertion rule/gone", assertion, TypeR53RCSafetyRule, r53rcTestGone, -1, "", false, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newR53RCCalls()
			calls.errs[c.arn] = c.err

			total, err := r53rcScan(context.Background(), st, calls, r53rcHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if want := r53rcTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			switch {
			case c.lost > 0 && ok:
				t.Fatal("row stored; want it dropped as deleted")
			case c.lost == 0 && !ok:
				t.Fatal("row not stored; want it kept")
			case ok && r.TagsJSON != nil:
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			for _, op := range []string{"ListRoutingControls", "ListSafetyRules"} {
				if c.lost > 0 && slices.Contains(calls.parentsR53RC(op), c.arn) {
					t.Errorf("%s called for the dropped %s", op, c.arn)
				}
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{r53rcTagsOp}
			}
			if got := r53rcWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+r53rcTestHome || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", x.Scope, x.Message, testAccountID, r53rcTestHome, c.arn)
				}
			}
		})
	}
}

// A row whose ARN names another account (a shared cluster, a panel or rule
// in one) is kept untagged without a warning when its tag read is denied or
// finds it gone; the same failure of this account's own row warns or drops
// it (TagReadFailures).
func TestScanR53RecoveryControl_OtherAccountRowKeptUntagged(t *testing.T) {
	t.Parallel()
	other := "210987654321"
	shared := r53rcTestARNIn(other, "cluster/c-shared")
	sharedPanel := r53rcTestARNIn(other, "controlpanel/p-shared")
	sharedRule := sharedPanel + "/safetyrule/sr-shared"
	for _, e := range []struct {
		name string
		err  error
	}{{"denied", r53rcTestDenied}, {"gone", r53rcTestGone}} {
		t.Run(e.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newR53RCCalls()
			handlers := r53rcHandlers(calls)
			handlers["ListClusters"] = func(any) (any, error) {
				return &route53recoverycontrolconfig.ListClustersOutput{Clusters: []r53rctypes.Cluster{{ClusterArn: sdkaws.String(shared), Owner: sdkaws.String(other)}}}, nil
			}
			handlers["ListControlPanels"] = func(any) (any, error) {
				return &route53recoverycontrolconfig.ListControlPanelsOutput{ControlPanels: []r53rctypes.ControlPanel{{ControlPanelArn: sdkaws.String(sharedPanel), Owner: sdkaws.String(other)}}}, nil
			}
			handlers["ListRoutingControls"] = func(any) (any, error) { return &route53recoverycontrolconfig.ListRoutingControlsOutput{}, nil }
			handlers["ListSafetyRules"] = func(any) (any, error) {
				return &route53recoverycontrolconfig.ListSafetyRulesOutput{SafetyRules: []r53rctypes.Rule{{GATING: &r53rctypes.GatingRule{SafetyRuleArn: sdkaws.String(sharedRule)}}}}, nil
			}
			for _, arn := range []string{shared, sharedPanel, sharedRule} {
				calls.errs[arn] = e.err
			}

			total, err := r53rcScan(context.Background(), st, calls, handlers)
			if err != nil || total != 3 {
				t.Fatalf("scan = %d, %v; want 3, nil", total, err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for typ, arn := range map[string]string{TypeR53RCCluster: shared, TypeR53RCControlPanel: sharedPanel, TypeR53RCSafetyRule: sharedRule} {
				r, ok := rowsByNativeID(t, st, typ)[arn]
				if !ok || r.TagsJSON != nil {
					t.Errorf("%s stored %v, TagsJSON %v; want stored untagged", arn, ok, r.TagsJSON)
				}
			}
		})
	}
}

// Denied tag reads in all three tagged phases send one warning for the whole
// scan; the rows are kept.
func TestScanR53RecoveryControl_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newR53RCCalls()
	for _, k := range r53rcKinds() {
		for _, l := range k.listedR53RC() {
			if k.tagged {
				calls.errs[k.native(l.panel, l.suffix)] = r53rcTestDenied
			}
		}
	}

	total, err := r53rcScan(context.Background(), st, calls, r53rcHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != r53rcTestTotal {
		t.Errorf("total = %d; want %d", total, r53rcTestTotal)
	}
	if got := r53rcWarnOps(warns()); !slices.Equal(got, []string{r53rcTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, r53rcTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanR53RecoveryControl_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, r53rcTagsOp)
	calls := newR53RCCalls()
	calls.errs[r53rcClusterARN("bare")] = r53rcTestThrottle
	calls.errs[r53rcKindOf(t, "ListSafetyRules").native(r53rcPanelARN("tagged"), "bare")] = r53rcTestBad

	_, err := r53rcScan(context.Background(), st, calls, r53rcHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", r53rcTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phase's rows
// stay stored.
func TestScanR53RecoveryControl_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeR53RCControlPanel)
	calls := newR53RCCalls()
	calls.errs[r53rcPanelARN("tagged")] = r53rcTestDenied

	total, err := r53rcScan(context.Background(), st, calls, r53rcHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListRoutingControls", "ListSafetyRules"} {
		if n := calls.calledR53RC(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeR53RCCluster)); total != 2 || n != 2 {
		t.Errorf("total = %d, clusters stored = %d; want 2, 2", total, n)
	}
}

// A listing's failure keeps the pages read before it and does not stop the
// later phases; a denied listing warns under its op instead of failing. A
// panel not stored has no children read.
func TestScanR53RecoveryControl_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	rules := r53rcKindOf(t, "ListSafetyRules")
	for _, c := range []struct {
		name, op, panel string
		// page2 fails the second page only; otherwise the first fails.
		page2      bool
		err        error
		kept, lost string
		// wantTotal is the rows stored; wantRules those of the last phase.
		wantTotal, wantRules int
		wantWarn             bool
	}{
		// A denied cluster listing: no cluster; the panels and their
		// children are still read.
		{"clusters denied", "ListClusters", "", false, r53rcTestDenied, "", r53rcClusterARN("tagged"), r53rcTestTotal - 2, 4, true},
		// The clusters' second page: c-bare is lost; the rest is read.
		{"clusters page 2", "ListClusters", "", true, r53rcTestBad, r53rcClusterARN("tagged"), r53rcClusterARN("bare"), r53rcTestTotal - 1, 4, false},
		// The panels' second page: p-bare and its 4 children are lost.
		{"control panels page 2", "ListControlPanels", "", true, r53rcTestBad, r53rcPanelARN("tagged"), r53rcPanelARN("bare"), r53rcTestTotal - 5, 2, false},
		// One panel's second page of safety rules: sr-bare is lost.
		{"safety rules page 2", "ListSafetyRules", r53rcPanelARN("tagged"), true, r53rcTestBad, rules.native(r53rcPanelARN("tagged"), "tagged"), rules.native(r53rcPanelARN("tagged"), "bare"), r53rcTestTotal - 1, 3, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newR53RCCalls()
			handlers := r53rcHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") != nil) == c.page2 && r53rcTestParent(in) == c.panel {
					calls.recordR53RC(c.op, in)
					return nil, c.err
				}
				return list(in)
			}

			total, err := r53rcScan(context.Background(), st, calls, handlers)
			if c.wantWarn {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, r53rcKindOf(t, c.op).typ)
			if _, ok := rows[c.kept]; c.kept != "" && !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := len(rowsByNativeID(t, st, TypeR53RCSafetyRule)); got != c.wantRules {
				t.Errorf("safety rules (the last phase) = %d; want %d", got, c.wantRules)
			}
			var wantWarns []string
			if c.wantWarn {
				wantWarns = []string{"route53recoverycontrolconfig:" + c.op}
			}
			if got := r53rcWarnOps(warns()); !slices.Equal(got, wantWarns) {
				t.Errorf("warnings = %v; want %v", got, wantWarns)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages; the
// later phases still run.
func TestScanR53RecoveryControl_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newR53RCCalls()
	handlers := r53rcHandlers(calls)
	twoPages := handlers["ListClusters"]
	handlers["ListClusters"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*route53recoverycontrolconfig.ListClustersOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, err := r53rcScan(context.Background(), st, calls, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != r53rcTestTotal {
		t.Errorf("total = %d; want %d", total, r53rcTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeR53RCSafetyRule)); n != 4 {
		t.Errorf("safety rules = %d; want 4", n)
	}
}

// One panel's failed child listing costs the other panel none of its rows:
// a panel deleted since it was listed (the modeled
// ResourceNotFoundException) is skipped silently, a denial warns once under
// the op, and any other error is returned.
func TestScanR53RecoveryControl_PerPanelFailures(t *testing.T) {
	t.Parallel()
	panel := r53rcPanelARN("tagged")
	for _, op := range []string{"ListRoutingControls", "ListSafetyRules"} {
		for _, e := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"panel gone", r53rcTestGone, "", 0},
			{"denied", r53rcTestDenied, "", 1},
			{"other error", r53rcTestBad, "ValidationException", 0},
		} {
			t.Run(op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newR53RCCalls()
				handlers := r53rcHandlers(calls)
				answer := handlers[op]
				handlers[op] = func(in any) (any, error) {
					if r53rcTestParent(in) == panel {
						calls.recordR53RC(op, in)
						return nil, e.err
					}
					return answer(in)
				}

				total, err := r53rcScan(context.Background(), st, calls, handlers)
				if e.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, e.code)
				}
				if want := r53rcTestTotal - 2; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				k := r53rcKindOf(t, op)
				for _, s := range []string{"tagged", "bare"} {
					if _, ok := rowsByNativeID(t, st, k.typ)[k.native(r53rcPanelARN("bare"), s)]; !ok {
						t.Errorf("the other panel's %s %s missing", k.typ, s)
					}
				}
				w := warns()
				if len(w) != e.wantWarns || (len(w) == 1 && (w[0].Service != "route53recoverycontrolconfig:"+op || !strings.Contains(w[0].Message, panel))) {
					t.Errorf("warnings = %v; want %d under route53recoverycontrolconfig:%s naming %s", w, e.wantWarns, op, panel)
				}
			})
		}
	}
}

// An element without an ARN (a safety rule with neither arm included) is
// not stored, is not sent to the tag op, has no children read, and is
// warned about once under its listing, naming it.
func TestScanR53RecoveryControl_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	panel := r53rcPanelARN("tagged")
	onlyUnder := func(in any, out, empty any) any {
		if r53rcTestParent(in) != panel {
			return empty
		}
		return out
	}
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) any
		wantTotal     int
		typ           string
		childless     bool
	}{
		{
			"cluster", "ListClusters", "c-x",
			func(any) any {
				return &route53recoverycontrolconfig.ListClustersOutput{Clusters: []r53rctypes.Cluster{{Name: sdkaws.String("c-x")}}}
			},
			r53rcTestTotal - 2, TypeR53RCCluster, false,
		},
		{
			"cluster without a name", "ListClusters", noIdentifier,
			func(any) any {
				return &route53recoverycontrolconfig.ListClustersOutput{Clusters: []r53rctypes.Cluster{{}}}
			},
			r53rcTestTotal - 2, TypeR53RCCluster, false,
		},
		{
			"control panel", "ListControlPanels", "p-x",
			func(any) any {
				return &route53recoverycontrolconfig.ListControlPanelsOutput{ControlPanels: []r53rctypes.ControlPanel{{Name: sdkaws.String("p-x")}}}
			},
			2, TypeR53RCControlPanel, true,
		},
		{
			"routing control", "ListRoutingControls", "rc-x",
			func(in any) any {
				return onlyUnder(in,
					&route53recoverycontrolconfig.ListRoutingControlsOutput{RoutingControls: []r53rctypes.RoutingControl{{Name: sdkaws.String("rc-x")}}},
					&route53recoverycontrolconfig.ListRoutingControlsOutput{})
			},
			r53rcTestTotal - 4, TypeR53RCRoutingControl, false,
		},
		{
			// With no name either, the warning names the control's panel.
			"routing control without a name", "ListRoutingControls", panel,
			func(in any) any {
				return onlyUnder(in,
					&route53recoverycontrolconfig.ListRoutingControlsOutput{RoutingControls: []r53rctypes.RoutingControl{{}}},
					&route53recoverycontrolconfig.ListRoutingControlsOutput{})
			},
			r53rcTestTotal - 4, TypeR53RCRoutingControl, false,
		},
		{
			// With no name either, the warning names the rule's panel.
			"safety rule with neither arm", "ListSafetyRules", panel,
			func(in any) any {
				return onlyUnder(in,
					&route53recoverycontrolconfig.ListSafetyRulesOutput{SafetyRules: []r53rctypes.Rule{{}}},
					&route53recoverycontrolconfig.ListSafetyRulesOutput{})
			},
			r53rcTestTotal - 4, TypeR53RCSafetyRule, false,
		},
		{
			"assertion rule", "ListSafetyRules", "sr-x",
			func(in any) any {
				return onlyUnder(in,
					&route53recoverycontrolconfig.ListSafetyRulesOutput{SafetyRules: []r53rctypes.Rule{{ASSERTION: &r53rctypes.AssertionRule{Name: sdkaws.String("sr-x")}}}},
					&route53recoverycontrolconfig.ListSafetyRulesOutput{})
			},
			r53rcTestTotal - 4, TypeR53RCSafetyRule, false,
		},
		{
			"gating rule", "ListSafetyRules", "sr-x",
			func(in any) any {
				return onlyUnder(in,
					&route53recoverycontrolconfig.ListSafetyRulesOutput{SafetyRules: []r53rctypes.Rule{{GATING: &r53rctypes.GatingRule{Name: sdkaws.String("sr-x")}}}},
					&route53recoverycontrolconfig.ListSafetyRulesOutput{})
			},
			r53rcTestTotal - 4, TypeR53RCSafetyRule, false,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newR53RCCalls()
			handlers := r53rcHandlers(calls)
			handlers[c.op] = func(in any) (any, error) {
				calls.recordR53RC(c.op, in)
				return c.answer(in), nil
			}

			total, err := r53rcScan(context.Background(), st, calls, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "route53recoverycontrolconfig:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under route53recoverycontrolconfig:%s naming %s", w, c.op, c.key)
			}
			if slices.Contains(calls.tagARNsR53RC(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
			if c.childless {
				for _, op := range []string{"ListRoutingControls", "ListSafetyRules"} {
					if n := calls.calledR53RC(op); n != 0 {
						t.Errorf("%s calls = %d, panels %v; want none", op, n, calls.parentsR53RC(op))
					}
				}
			}
			if rows := rowsByNativeID(t, st, c.typ); len(rows) != 0 {
				t.Errorf("%s rows = %v; want none", c.typ, slices.Sorted(maps.Keys(rows)))
			}
		})
	}
}

// In GovCloud and China the pinned SDK confirms no home for the service, so
// the scan makes no call and reports the service unavailable there: no
// listed ARN of those partitions reaches the tag op.
func TestScanR53RecoveryControl_PartitionsWithoutHomeMakeNoCall(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newR53RCCalls()
			total, err := r53rcScanIn(context.Background(), st, partitionAccount(pf.partition), calls, r53rcHandlers(calls))
			if !errors.Is(err, errServiceUnavailable) || total != 0 {
				t.Fatalf("scan = %d, %v; want 0, errServiceUnavailable", total, err)
			}
			if len(calls.regions) != 0 {
				t.Errorf("calls reached %v; want none", calls.regions)
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanR53RecoveryControl_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newR53RCCalls()
	handlers := r53rcHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListControlPanels"]
	handlers["ListControlPanels"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, err := r53rcScan(ctx, st, calls, handlers)
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
	for _, op := range []string{"ListRoutingControls", "ListSafetyRules"} {
		if n := calls.calledR53RC(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanR53RecoveryControl_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "route53-recovery-control-config.us-west-2.amazonaws.com", IsNotFound: true}
	t.Run("last phase after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		calls := newR53RCCalls()
		handlers := r53rcHandlers(calls)
		handlers["ListSafetyRules"] = func(any) (any, error) { return nil, nx }
		total, err := r53rcScan(context.Background(), st, calls, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := r53rcTestTotal - 4; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newR53RCCalls()
		calls.errs[r53rcClusterARN("bare")] = nx
		_, err := r53rcScan(context.Background(), st, calls, r53rcHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, err := scanR53RecoveryControl(context.Background(), acct, "", st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
