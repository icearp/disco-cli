package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// elbTestTotal is every row of the fixture: two load balancers, target
	// groups and trust stores (one per page); one revocation per trust store;
	// two listeners (one per page) per load balancer; two rules (one per
	// page) and one certificate per listener.
	elbTestTotal = 2 + 2 + 2 + 2 + 2*2 + 4*2 + 4
)

var (
	elbTestDenied   = apiErr("AccessDenied", "User: arn:aws:iam::123:user/x is not authorized to perform: elasticloadbalancing:DescribeTags")
	elbTestThrottle = apiErr("Throttling", "Rate exceeded")
	elbTestBad      = apiErr("ValidationError", "bad")
	elbTestCreated  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// elbFix builds the fixture's ARNs in one partition and region.
type elbFix struct{ part, region string }

var elbCommercial = elbFix{"aws", testRegion}

func (f elbFix) arn(res string) string {
	return "arn:" + f.part + ":elasticloadbalancing:" + f.region + ":" + testAccountID + ":" + res
}

func (f elbFix) lb(name string) string {
	return f.arn("loadbalancer/app/" + name + "/50dc6c495c0c9188")
}
func (f elbFix) tg(name string) string { return f.arn("targetgroup/" + name + "/73e2d6bc24d8a067") }
func (f elbFix) ts(name string) string { return f.arn("truststore/" + name + "/a1b2c3d4e5f60718") }

func (f elbFix) cert() string {
	return "arn:" + f.part + ":acm:" + f.region + ":" + testAccountID + ":certificate/c-1"
}

// listener is the ARN of listener name of the load balancer lbARN.
func elbTestListener(lbARN, name string) string {
	return strings.Replace(lbARN, ":loadbalancer/", ":listener/", 1) + "/" + name
}

// elbTestRule is the ARN of rule name of the listener listenerARN.
func elbTestRule(listenerARN, name string) string {
	return strings.Replace(listenerARN, ":listener/", ":listener-rule/", 1) + "/" + name
}

// elbTestPage is a two-page listing's page: the "tagged" element first, then
// the "bare" one, with the marker that follows it.
func elbTestPage(in any) (suffix string, next *string) {
	if bacField[*string](in, "Marker") == nil {
		return "tagged", sdkaws.String("p2")
	}
	return "bare", nil
}

// elbCalls records a scan's calls and answers DescribeTags, failing a call
// with errs of any ARN it is sent, and leaving out of the response the ARNs
// in missing. The "-tagged" rows are tagged with their name.
type elbCalls struct {
	f         elbFix
	mu        sync.Mutex
	pageSizes map[string][]int32
	parents   map[string][]string
	tagCalls  [][]string
	errs      map[string]error
	missing   map[string]bool
}

func newELBCalls(f elbFix) *elbCalls {
	return &elbCalls{f: f, pageSizes: map[string][]int32{}, parents: map[string][]string{}, errs: map[string]error{}, missing: map[string]bool{}}
}

// record notes op's PageSize and, on a first page, the parent it lists
// (field names the parent's ARN; "" for none).
func (c *elbCalls) record(op, field string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := bacField[*int32](in, "PageSize"); n != nil {
		c.pageSizes[op] = append(c.pageSizes[op], *n)
	} else {
		c.pageSizes[op] = append(c.pageSizes[op], 0)
	}
	if field != "" && bacField[*string](in, "Marker") == nil {
		c.parents[op] = append(c.parents[op], sv(bacField[*string](in, field)))
	}
}

func (c *elbCalls) listedParents(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.parents[op]))
}

func (c *elbCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pageSizes[op])
}

// tagARNs is every ARN DescribeTags was sent, sorted.
func (c *elbCalls) tagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var arns []string
	for _, call := range c.tagCalls {
		arns = append(arns, call...)
	}
	slices.Sort(arns)
	return arns
}

func (c *elbCalls) describeTags(in any) (any, error) {
	arns := in.(*elasticloadbalancingv2.DescribeTagsInput).ResourceArns
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagCalls = append(c.tagCalls, slices.Clone(arns))
	for _, arn := range arns {
		if err := c.errs[arn]; err != nil {
			return nil, err
		}
	}
	out := &elasticloadbalancingv2.DescribeTagsOutput{}
	for _, arn := range arns {
		if c.missing[arn] {
			continue
		}
		d := elbv2types.TagDescription{ResourceArn: sdkaws.String(arn), Tags: []elbv2types.Tag{}}
		if name := elbTestOwnName(arn); strings.HasSuffix(name, "-tagged") {
			d.Tags = append(d.Tags, elbv2types.Tag{Key: sdkaws.String("name"), Value: sdkaws.String(name)})
		}
		out.TagDescriptions = append(out.TagDescriptions, d)
	}
	return out, nil
}

// elbTestOwnName is the name of the row arn names: the last segment of a
// listener's or rule's ARN, the name segment of the others'.
func elbTestOwnName(arn string) string {
	parts := strings.Split(arn[strings.Index(arn, ":"+testAccountID+":")+len(testAccountID)+2:], "/")
	switch parts[0] {
	case "listener", "listener-rule":
		return parts[len(parts)-1]
	case "loadbalancer":
		return parts[2]
	}
	return parts[1]
}

// elbHandlers answers every op of the scan: each listing in two pages (one
// page of one element for revocations and certificates), DescribeTargetHealth
// with one target, and DescribeTags from c.
func elbHandlers(c *elbCalls) map[string]func(in any) (any, error) {
	f := c.f
	return map[string]func(in any) (any, error){
		"DescribeTags": c.describeTags,
		"DescribeLoadBalancers": func(in any) (any, error) {
			c.record("DescribeLoadBalancers", "", in)
			suffix, next := elbTestPage(in)
			name := "lb-" + suffix
			return &elasticloadbalancingv2.DescribeLoadBalancersOutput{NextMarker: next, LoadBalancers: []elbv2types.LoadBalancer{{
				LoadBalancerArn: sdkaws.String(f.lb(name)), LoadBalancerName: sdkaws.String(name),
				Type: elbv2types.LoadBalancerTypeEnumApplication, CreatedTime: sdkaws.Time(elbTestCreated),
				State: &elbv2types.LoadBalancerState{Code: elbv2types.LoadBalancerStateEnumActive},
			}}}, nil
		},
		"DescribeTargetGroups": func(in any) (any, error) {
			c.record("DescribeTargetGroups", "", in)
			suffix, next := elbTestPage(in)
			name := "tg-" + suffix
			return &elasticloadbalancingv2.DescribeTargetGroupsOutput{NextMarker: next, TargetGroups: []elbv2types.TargetGroup{{
				TargetGroupArn: sdkaws.String(f.tg(name)), TargetGroupName: sdkaws.String(name), Port: sdkaws.Int32(80),
			}}}, nil
		},
		"DescribeTargetHealth": func(in any) (any, error) {
			c.record("DescribeTargetHealth", "TargetGroupArn", in)
			tg := sv(in.(*elasticloadbalancingv2.DescribeTargetHealthInput).TargetGroupArn)
			return &elasticloadbalancingv2.DescribeTargetHealthOutput{TargetHealthDescriptions: []elbv2types.TargetHealthDescription{
				{Target: &elbv2types.TargetDescription{Id: sdkaws.String("i-" + elbTestOwnName(tg)), Port: sdkaws.Int32(80)}},
				{}, // no Target: not stored
			}}, nil
		},
		"DescribeTrustStores": func(in any) (any, error) {
			c.record("DescribeTrustStores", "", in)
			suffix, next := elbTestPage(in)
			name := "ts-" + suffix
			return &elasticloadbalancingv2.DescribeTrustStoresOutput{NextMarker: next, TrustStores: []elbv2types.TrustStore{{
				TrustStoreArn: sdkaws.String(f.ts(name)), Name: sdkaws.String(name), Status: elbv2types.TrustStoreStatusActive,
			}}}, nil
		},
		"DescribeTrustStoreRevocations": func(in any) (any, error) {
			c.record("DescribeTrustStoreRevocations", "TrustStoreArn", in)
			ts := bacField[*string](in, "TrustStoreArn")
			return &elasticloadbalancingv2.DescribeTrustStoreRevocationsOutput{TrustStoreRevocations: []elbv2types.DescribeTrustStoreRevocation{{
				TrustStoreArn: ts, RevocationId: sdkaws.Int64(7), NumberOfRevokedEntries: sdkaws.Int64(3),
			}}}, nil
		},
		"DescribeListeners": func(in any) (any, error) {
			c.record("DescribeListeners", "LoadBalancerArn", in)
			suffix, next := elbTestPage(in)
			lb := bacField[*string](in, "LoadBalancerArn")
			return &elasticloadbalancingv2.DescribeListenersOutput{NextMarker: next, Listeners: []elbv2types.Listener{{
				ListenerArn: sdkaws.String(elbTestListener(sv(lb), "l-"+suffix)), LoadBalancerArn: lb, Port: sdkaws.Int32(443),
			}}}, nil
		},
		"DescribeRules": func(in any) (any, error) {
			c.record("DescribeRules", "ListenerArn", in)
			suffix, next := elbTestPage(in)
			listener := sv(bacField[*string](in, "ListenerArn"))
			return &elasticloadbalancingv2.DescribeRulesOutput{NextMarker: next, Rules: []elbv2types.Rule{{
				RuleArn: sdkaws.String(elbTestRule(listener, "r-"+suffix)), Priority: sdkaws.String("1"),
			}}}, nil
		},
		"DescribeListenerCertificates": func(in any) (any, error) {
			c.record("DescribeListenerCertificates", "ListenerArn", in)
			return &elasticloadbalancingv2.DescribeListenerCertificatesOutput{Certificates: []elbv2types.Certificate{{
				CertificateArn: sdkaws.String(f.cert()), IsDefault: sdkaws.Bool(true),
			}}}, nil
		},
	}
}

// elbScan runs the scan's real entry point in acct's partition and the
// fixture's region against handlers; every op they do not answer is denied.
func elbScan(ctx context.Context, st *store.Store, acct *account, f elbFix, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct.cfg = opStubConfig(handlers, elbTestDenied)
	return scanELBv2(ctx, acct, f.region, st, testScanID)
}

func elbScanCommercial(st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	return elbScan(context.Background(), st, newTestAccount(testAccountID), elbCommercial, handlers)
}

// elbTestRows is the NativeID of every row of the fixture, by type.
func elbTestRows(f elbFix) map[string][]string {
	rows := map[string][]string{}
	for _, s := range []string{"tagged", "bare"} {
		lb := f.lb("lb-" + s)
		rows[TypeELBv2LoadBalancer] = append(rows[TypeELBv2LoadBalancer], lb)
		rows[TypeELBv2TargetGroup] = append(rows[TypeELBv2TargetGroup], f.tg("tg-"+s))
		rows[TypeELBv2TrustStore] = append(rows[TypeELBv2TrustStore], f.ts("ts-"+s))
		rows[TypeELBv2TrustStoreRevocation] = append(rows[TypeELBv2TrustStoreRevocation], f.ts("ts-"+s)+":rev/7")
		for _, ls := range []string{"tagged", "bare"} {
			l := elbTestListener(lb, "l-"+ls)
			rows[TypeELBv2Listener] = append(rows[TypeELBv2Listener], l)
			rows[TypeELBv2ListenerCertificate] = append(rows[TypeELBv2ListenerCertificate], l+":cert/"+f.cert())
			for _, rs := range []string{"tagged", "bare"} {
				rows[TypeELBv2ListenerRule] = append(rows[TypeELBv2ListenerRule], elbTestRule(l, "r-"+rs))
			}
		}
	}
	for typ := range rows {
		slices.Sort(rows[typ])
	}
	return rows
}

// elbTestTaggedTypes are the types whose tags are read.
var elbTestTaggedTypes = []string{TypeELBv2LoadBalancer, TypeELBv2TargetGroup, TypeELBv2TrustStore, TypeELBv2Listener, TypeELBv2ListenerRule}

// elbTestTagARNs is the ARN of every row whose tags are read, sorted.
func elbTestTagARNs(f elbFix) []string {
	var arns []string
	rows := elbTestRows(f)
	for _, typ := range elbTestTaggedTypes {
		arns = append(arns, rows[typ]...)
	}
	slices.Sort(arns)
	return arns
}

func elbWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, for every parent stored, each row
// under its NativeID, with its name, status, creation time and attributes,
// and the tagged types with the tags DescribeTags returns (nil for none).
// DescribeTags is sent exactly the ARNs of every tagged row; every listing
// is sent PageSize 400. No error, no warning.
func TestScanELBv2_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newELBCalls(elbCommercial)

	total, inserted, err := elbScanCommercial(st, elbHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != elbTestTotal || inserted != elbTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, elbTestTotal, elbTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	want := elbTestRows(elbCommercial)
	assertELBTestRows(t, st, want)
	for _, typ := range []string{TypeELBv2TrustStoreRevocation, TypeELBv2ListenerCertificate} {
		for id, r := range rowsByNativeID(t, st, typ) {
			if r.TagsJSON != nil || r.Name != nil || r.Status != nil || r.CreatedAt != nil {
				t.Errorf("%s TagsJSON, Name, Status, CreatedAt = %v, %v, %v, %v; want all nil", id, r.TagsJSON, r.Name, r.Status, r.CreatedAt)
			}
		}
	}
	if got := calls.tagARNs(); !slices.Equal(got, elbTestTagARNs(elbCommercial)) {
		t.Errorf("DescribeTags ARNs = %v\nwant %v", got, elbTestTagARNs(elbCommercial))
	}
	for _, op := range []string{"DescribeLoadBalancers", "DescribeTargetGroups", "DescribeTrustStores", "DescribeTrustStoreRevocations", "DescribeListeners", "DescribeRules", "DescribeListenerCertificates"} {
		if calls.count(op) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range calls.pageSizes[op] {
			if n != 400 {
				t.Errorf("%s PageSize = %d; want 400", op, n)
			}
		}
	}
	if got := calls.listedParents("DescribeTargetHealth"); !slices.Equal(got, want[TypeELBv2TargetGroup]) {
		t.Errorf("DescribeTargetHealth parents = %v\nwant %v", got, want[TypeELBv2TargetGroup])
	}
	assertELBTestChildParents(t, calls, want)
}

// assertELBTestRows checks that every type's rows are want's, and that each
// tagged type's "-tagged" rows have the tags DescribeTags returns and the
// others none.
func assertELBTestRows(t *testing.T, st *store.Store, want map[string][]string) {
	t.Helper()
	for typ, ids := range want {
		if got := storedNativeIDs(t, st, typ); !slices.Equal(got, ids) {
			t.Errorf("%s rows = %v\nwant %v", typ, got, ids)
		}
	}
	for _, typ := range elbTestTaggedTypes {
		for id, r := range rowsByNativeID(t, st, typ) {
			if name := elbTestOwnName(id); strings.HasSuffix(name, "-tagged") {
				assertTagsMap(t, r.TagsJSON, map[string]string{"name": name})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", id, *r.TagsJSON)
			}
		}
	}
}

// assertELBTestChildParents checks that each child listing was read for
// exactly the parents in want.
func assertELBTestChildParents(t *testing.T, calls *elbCalls, want map[string][]string) {
	t.Helper()
	listeners := want[TypeELBv2Listener]
	for op, parents := range map[string][]string{
		"DescribeTrustStoreRevocations": want[TypeELBv2TrustStore],
		"DescribeListeners":             want[TypeELBv2LoadBalancer],
		"DescribeRules":                 listeners,
		"DescribeListenerCertificates":  listeners,
	} {
		if got := calls.listedParents(op); !slices.Equal(got, parents) {
			t.Errorf("%s parents = %v\nwant %v", op, got, parents)
		}
	}
}

// Each row builder keeps base's identity: the name, status and creation time
// it sets, and its attributes (the load balancer and rule wrappers, the
// target group with its targets, the other elements verbatim). A load
// balancer's Status is its state code, empty when the code is, and nil
// when it has no State.
func TestScanELBv2_RowFields(t *testing.T) {
	st := newTestStore(t)
	f := elbCommercial
	handlers := elbHandlers(newELBCalls(f))
	listLBs := handlers["DescribeLoadBalancers"]
	handlers["DescribeLoadBalancers"] = func(in any) (any, error) {
		out, err := listLBs(in)
		if bacField[*string](in, "Marker") == nil {
			o := out.(*elasticloadbalancingv2.DescribeLoadBalancersOutput)
			for name, state := range map[string]*elbv2types.LoadBalancerState{"lb-nostate": nil, "lb-nocode": {}} {
				o.LoadBalancers = append(o.LoadBalancers, elbv2types.LoadBalancer{
					LoadBalancerArn: sdkaws.String(f.lb(name)), LoadBalancerName: sdkaws.String(name),
					Type: elbv2types.LoadBalancerTypeEnumApplication, CreatedTime: sdkaws.Time(elbTestCreated), State: state,
				})
			}
		}
		return out, err
	}
	if _, _, err := elbScanCommercial(st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	lbs := rowsByNativeID(t, st, TypeELBv2LoadBalancer)
	for name, status := range map[string]*string{"lb-tagged": sdkaws.String("active"), "lb-nocode": sdkaws.String(""), "lb-nostate": nil} {
		lb, ok := lbs[f.lb(name)]
		if !ok {
			t.Fatalf("%s not stored", name)
		}
		if sv(lb.Name) != name || (lb.Status == nil) != (status == nil) || sv(lb.Status) != sv(status) || sv(lb.CreatedAt) != "2026-01-02T03:04:05Z" || sv(lb.Region) != testRegion {
			t.Errorf("%s Name, Status, CreatedAt, Region = %q, %v, %q, %q; want %s, %v, 2026-01-02T03:04:05Z, %s", name, sv(lb.Name), lb.Status, sv(lb.CreatedAt), sv(lb.Region), name, status, testRegion)
		}
		var lbAttrs struct {
			LB   elbv2types.LoadBalancer `json:"lb"`
			Type string                  `json:"type"`
		}
		if err := json.Unmarshal([]byte(lb.AttributesJSON), &lbAttrs); err != nil || sv(lbAttrs.LB.LoadBalancerArn) != f.lb(name) || lbAttrs.Type != "application" {
			t.Errorf("%s attrs = %s (%v); want the lb wrapper", name, lb.AttributesJSON, err)
		}
	}

	tg := rowsByNativeID(t, st, TypeELBv2TargetGroup)[f.tg("tg-bare")]
	if sv(tg.Name) != "tg-bare" || tg.Status != nil || tg.CreatedAt != nil {
		t.Errorf("target group Name, Status, CreatedAt = %q, %v, %v; want tg-bare, nil, nil", sv(tg.Name), tg.Status, tg.CreatedAt)
	}
	var tgAttrs struct {
		TargetGroup elbv2types.TargetGroup
		Targets     []elbv2types.TargetDescription
	}
	if err := json.Unmarshal([]byte(tg.AttributesJSON), &tgAttrs); err != nil || sv(tgAttrs.TargetGroup.TargetGroupArn) != f.tg("tg-bare") ||
		len(tgAttrs.Targets) != 1 || sv(tgAttrs.Targets[0].Id) != "i-tg-bare" {
		t.Errorf("target group attrs = %s (%v); want the element and its one target", tg.AttributesJSON, err)
	}

	ts := rowsByNativeID(t, st, TypeELBv2TrustStore)[f.ts("ts-tagged")]
	var tsAttrs elbv2types.TrustStore
	if err := json.Unmarshal([]byte(ts.AttributesJSON), &tsAttrs); err != nil || sv(ts.Name) != "ts-tagged" || ts.Status != nil || sv(tsAttrs.TrustStoreArn) != f.ts("ts-tagged") || tsAttrs.Status != elbv2types.TrustStoreStatusActive {
		t.Errorf("trust store Name, Status, attrs = %q, %v, %s (%v); want ts-tagged, nil, the element", sv(ts.Name), ts.Status, ts.AttributesJSON, err)
	}

	listenerARN := elbTestListener(f.lb("lb-bare"), "l-tagged")
	l := rowsByNativeID(t, st, TypeELBv2Listener)[listenerARN]
	var lAttrs elbv2types.Listener
	if err := json.Unmarshal([]byte(l.AttributesJSON), &lAttrs); err != nil || l.Name != nil || sv(lAttrs.LoadBalancerArn) != f.lb("lb-bare") || sv(lAttrs.ListenerArn) != listenerARN {
		t.Errorf("listener Name, attrs = %v, %s (%v); want nil, the element", l.Name, l.AttributesJSON, err)
	}

	ruleARN := elbTestRule(listenerARN, "r-bare")
	rule := rowsByNativeID(t, st, TypeELBv2ListenerRule)[ruleARN]
	var ruleAttrs struct {
		Rule        elbv2types.Rule `json:"rule"`
		ListenerArn string          `json:"listenerArn"`
	}
	if err := json.Unmarshal([]byte(rule.AttributesJSON), &ruleAttrs); err != nil || rule.Name != nil || sv(ruleAttrs.Rule.RuleArn) != ruleARN || ruleAttrs.ListenerArn != listenerARN {
		t.Errorf("rule Name, attrs = %v, %s (%v); want nil, the rule wrapper", rule.Name, rule.AttributesJSON, err)
	}

	cert := rowsByNativeID(t, st, TypeELBv2ListenerCertificate)[listenerARN+":cert/"+f.cert()]
	var certAttrs struct {
		ListenerArn string                 `json:"listenerArn"`
		Cert        elbv2types.Certificate `json:"cert"`
	}
	if err := json.Unmarshal([]byte(cert.AttributesJSON), &certAttrs); err != nil || certAttrs.ListenerArn != listenerARN || sv(certAttrs.Cert.CertificateArn) != f.cert() {
		t.Errorf("certificate attrs = %s (%v); want the certificate wrapper", cert.AttributesJSON, err)
	}

	rev := rowsByNativeID(t, st, TypeELBv2TrustStoreRevocation)[f.ts("ts-bare")+":rev/7"]
	var revAttrs elbv2types.DescribeTrustStoreRevocation
	if err := json.Unmarshal([]byte(rev.AttributesJSON), &revAttrs); err != nil || sv(revAttrs.TrustStoreArn) != f.ts("ts-bare") || sdkaws.ToInt64(revAttrs.NumberOfRevokedEntries) != 3 {
		t.Errorf("revocation attrs = %s (%v); want the element", rev.AttributesJSON, err)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanELBv2_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := elbScanCommercial(st, elbHandlers(newELBCalls(elbCommercial))); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := elbScanCommercial(st, elbHandlers(newELBCalls(elbCommercial)))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != elbTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, elbTestTotal)
	}
}

// Empty listings store nothing and call no child listing or DescribeTags.
func TestScanELBv2_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newELBCalls(elbCommercial)
	handlers := elbHandlers(calls)
	handlers["DescribeLoadBalancers"] = func(in any) (any, error) {
		calls.record("DescribeLoadBalancers", "", in)
		return &elasticloadbalancingv2.DescribeLoadBalancersOutput{}, nil
	}
	handlers["DescribeTargetGroups"] = func(in any) (any, error) {
		calls.record("DescribeTargetGroups", "", in)
		return &elasticloadbalancingv2.DescribeTargetGroupsOutput{}, nil
	}
	handlers["DescribeTrustStores"] = func(in any) (any, error) {
		calls.record("DescribeTrustStores", "", in)
		return &elasticloadbalancingv2.DescribeTrustStoresOutput{}, nil
	}

	total, inserted, err := elbScanCommercial(st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"DescribeTargetHealth", "DescribeTrustStoreRevocations", "DescribeListeners", "DescribeRules", "DescribeListenerCertificates"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if got := calls.tagARNs(); len(got) != 0 {
		t.Errorf("DescribeTags sent %v; want no call", got)
	}
}

// A page of 45 load balancers has its tags read in three DescribeTags calls
// of at most 20 ARNs, each ARN sent once.
func TestScanELBv2_TagReadsBatchTwentyARNs(t *testing.T) {
	st := newTestStore(t)
	calls := newELBCalls(elbCommercial)
	handlers := elbHandlers(calls)
	var lbs []elbv2types.LoadBalancer
	var want []string
	for i := range 45 {
		arn := elbCommercial.lb("lb-" + strconv.Itoa(i))
		want = append(want, arn)
		lbs = append(lbs, elbv2types.LoadBalancer{LoadBalancerArn: sdkaws.String(arn), LoadBalancerName: sdkaws.String("lb-" + strconv.Itoa(i))})
	}
	slices.Sort(want)
	handlers["DescribeLoadBalancers"] = func(any) (any, error) {
		return &elasticloadbalancingv2.DescribeLoadBalancersOutput{LoadBalancers: lbs}, nil
	}
	handlers["DescribeListeners"] = func(any) (any, error) { return &elasticloadbalancingv2.DescribeListenersOutput{}, nil }

	if _, _, err := elbScanCommercial(st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeELBv2LoadBalancer)); n != 45 {
		t.Errorf("load balancers = %d; want 45", n)
	}
	var sizes []int
	var sent []string
	for _, call := range calls.tagCalls {
		if strings.Contains(call[0], ":loadbalancer/") {
			sizes = append(sizes, len(call))
			sent = append(sent, call...)
		}
	}
	slices.Sort(sizes)
	slices.Sort(sent)
	if !slices.Equal(sizes, []int{5, 20, 20}) {
		t.Errorf("DescribeTags call sizes = %v; want [5 20 20]", sizes)
	}
	if !slices.Equal(sent, want) {
		t.Errorf("DescribeTags load balancer ARNs = %v\nwant %v", sent, want)
	}
}

// Every DescribeTags call of a scan takes one of 10 shared slots: two load
// balancers' listener pages of 200 listeners (ten calls each), read
// concurrently, never have more than 10 calls in flight, and reach 10. The
// listener calls wait until 10 calls are in flight.
func TestScanELBv2_TagReadsShareOneBound(t *testing.T) {
	st := newTestStore(t)
	calls := newELBCalls(elbCommercial)
	handlers := elbHandlers(calls)
	handlers["DescribeListeners"] = func(in any) (any, error) {
		lb := sv(bacField[*string](in, "LoadBalancerArn"))
		out := &elasticloadbalancingv2.DescribeListenersOutput{}
		for i := range 200 {
			out.Listeners = append(out.Listeners, elbv2types.Listener{ListenerArn: sdkaws.String(elbTestListener(lb, "l-"+strconv.Itoa(i)))})
		}
		return out, nil
	}
	handlers["DescribeRules"] = func(any) (any, error) { return &elasticloadbalancingv2.DescribeRulesOutput{}, nil }
	handlers["DescribeListenerCertificates"] = func(any) (any, error) {
		return &elasticloadbalancingv2.DescribeListenerCertificatesOutput{}, nil
	}
	var (
		mu             sync.Mutex
		inFlight, peak int
		once           sync.Once
	)
	full := make(chan struct{})
	handlers["DescribeTags"] = func(in any) (any, error) {
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		if inFlight == 10 {
			once.Do(func() { close(full) })
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			inFlight--
			mu.Unlock()
		}()
		if strings.Contains(in.(*elasticloadbalancingv2.DescribeTagsInput).ResourceArns[0], ":listener/") {
			<-full
		}
		return calls.describeTags(in)
	}

	total, _, err := elbScanCommercial(st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// Two each of load balancers, target groups, trust stores and
	// revocations, and the 400 listeners.
	if total != 408 {
		t.Errorf("total = %d; want 408", total)
	}
	if peak != 10 {
		t.Errorf("peak DescribeTags calls in flight = %d; want 10", peak)
	}
}

// A DescribeTags call that fails with a not-found code is made again for
// each ARN alone. For each tagged type, an element listed beside the
// "-tagged" one on each parent's first page and since deleted (its own
// not-found code) is dropped silently, with no child listing read for it,
// and every other row keeps its tags. No error, no warning.
func TestScanELBv2_NotFoundInBatchReadsEachARN(t *testing.T) {
	f := elbCommercial
	rows := elbTestRows(f)
	for _, c := range []struct {
		op, code string
		// seg is the resource segment of the type's ARNs.
		seg string
		// field names the listing's parent ARN in its input ("" for none);
		// parents are those of the fixture ([""] for none).
		field   string
		parents []string
		// arn is the ARN of the element named name under parent.
		arn func(parent, name string) string
		// add appends the element with arn to a listing's output.
		add func(out any, arn string)
	}{
		{
			"DescribeLoadBalancers", "LoadBalancerNotFound", ":loadbalancer/", "",
			[]string{""},
			func(_, name string) string { return f.lb("lb-" + name) },
			func(out any, arn string) {
				o := out.(*elasticloadbalancingv2.DescribeLoadBalancersOutput)
				o.LoadBalancers = append(o.LoadBalancers, elbv2types.LoadBalancer{LoadBalancerArn: sdkaws.String(arn), LoadBalancerName: sdkaws.String("lb-gone")})
			},
		},
		{
			"DescribeTargetGroups", "TargetGroupNotFound", ":targetgroup/", "",
			[]string{""},
			func(_, name string) string { return f.tg("tg-" + name) },
			func(out any, arn string) {
				o := out.(*elasticloadbalancingv2.DescribeTargetGroupsOutput)
				o.TargetGroups = append(o.TargetGroups, elbv2types.TargetGroup{TargetGroupArn: sdkaws.String(arn), TargetGroupName: sdkaws.String("tg-gone")})
			},
		},
		{
			"DescribeTrustStores", "TrustStoreNotFound", ":truststore/", "",
			[]string{""},
			func(_, name string) string { return f.ts("ts-" + name) },
			func(out any, arn string) {
				o := out.(*elasticloadbalancingv2.DescribeTrustStoresOutput)
				o.TrustStores = append(o.TrustStores, elbv2types.TrustStore{TrustStoreArn: sdkaws.String(arn), Name: sdkaws.String("ts-gone")})
			},
		},
		{
			"DescribeListeners", "ListenerNotFound", ":listener/", "LoadBalancerArn", rows[TypeELBv2LoadBalancer],
			func(lb, name string) string { return elbTestListener(lb, "l-"+name) },
			func(out any, arn string) {
				o := out.(*elasticloadbalancingv2.DescribeListenersOutput)
				o.Listeners = append(o.Listeners, elbv2types.Listener{ListenerArn: sdkaws.String(arn)})
			},
		},
		{
			"DescribeRules", "RuleNotFound", ":listener-rule/", "ListenerArn", rows[TypeELBv2Listener],
			func(listener, name string) string { return elbTestRule(listener, "r-"+name) },
			func(out any, arn string) {
				o := out.(*elasticloadbalancingv2.DescribeRulesOutput)
				o.Rules = append(o.Rules, elbv2types.Rule{RuleArn: sdkaws.String(arn)})
			},
		},
	} {
		t.Run(c.code, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newELBCalls(f)
			var wantCalls [][]string
			for _, p := range c.parents {
				tagged, gone, bare := c.arn(p, "tagged"), c.arn(p, "gone"), c.arn(p, "bare")
				calls.errs[gone] = apiErr(c.code, "gone")
				wantCalls = append(wantCalls, []string{tagged, gone}, []string{tagged}, []string{gone}, []string{bare})
			}
			handlers := elbHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				out, err := list(in)
				if bacField[*string](in, "Marker") == nil {
					parent := ""
					if c.field != "" {
						parent = sv(bacField[*string](in, c.field))
					}
					c.add(out, c.arn(parent, "gone"))
				}
				return out, err
			}

			total, _, err := elbScanCommercial(st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != elbTestTotal {
				t.Errorf("total = %d; want %d", total, elbTestTotal)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			assertELBTestRows(t, st, rows)
			assertELBTestChildParents(t, calls, rows)
			var got [][]string
			for _, call := range calls.tagCalls {
				if strings.Contains(call[0], c.seg) {
					got = append(got, call)
				}
			}
			cmp := func(a, b []string) int { return strings.Compare(strings.Join(a, " "), strings.Join(b, " ")) }
			slices.SortFunc(got, cmp)
			slices.SortFunc(wantCalls, cmp)
			if !slices.EqualFunc(got, wantCalls, slices.Equal) {
				t.Errorf("DescribeTags calls = %v\nwant %v", got, wantCalls)
			}
		})
	}
}

// A failed tag read. A denial keeps the row untagged and warns once under
// the tag op with the scan's scope; a throttle keeps it and is transient;
// any other error, and a response that leaves the resource out, keep it and
// are errors; a rule DescribeTags reports deleted is dropped silently.
func TestScanELBv2_TagReadFailures(t *testing.T) {
	rule := elbTestRule(elbTestListener(elbCommercial.lb("lb-bare"), "l-tagged"), "r-tagged")
	for _, c := range []struct {
		name      string
		err       error
		missing   bool
		outcome   serviceOutcome // -1: no error
		code      string
		sentinel  error
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"not found", apiErr("RuleNotFound", "gone"), false, -1, "", nil, elbTestTotal - 1, false, false},
		{"denied", elbTestDenied, false, -1, "", nil, elbTestTotal, true, true},
		{"transient", elbTestThrottle, false, outcomeTransient, "Throttling", nil, elbTestTotal, true, false},
		{"other error", elbTestBad, false, outcomeError, "ValidationError", nil, elbTestTotal, true, false},
		{"not described", nil, true, outcomeError, "", errTagsNotDescribed, elbTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newELBCalls(elbCommercial)
			if c.err != nil {
				calls.errs[rule] = c.err
			}
			calls.missing[rule] = c.missing

			total, _, err := elbScanCommercial(st, elbHandlers(calls))
			switch {
			case c.outcome < 0:
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			default:
				assertGlueOutcome(t, err, c.outcome)
			}
			if c.code != "" {
				assertAPICode(t, err, c.code)
			}
			if c.sentinel != nil && !errors.Is(err, c.sentinel) {
				t.Errorf("err = %v; want %v", err, c.sentinel)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, ok := rowsByNativeID(t, st, TypeELBv2ListenerRule)[rule]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{elbTagsOp}
			}
			if got := elbWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanELBv2_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newELBCalls(elbCommercial)
	rows := elbTestRows(elbCommercial)
	calls.errs[rows[TypeELBv2TargetGroup][0]] = elbTestDenied
	calls.errs[rows[TypeELBv2ListenerRule][3]] = elbTestDenied

	total, _, err := elbScanCommercial(st, elbHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != elbTestTotal {
		t.Errorf("total = %d; want %d", total, elbTestTotal)
	}
	if got := elbWarnOps(warns()); !slices.Equal(got, []string{elbTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, elbTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanELBv2_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, elbTagsOp)
	calls := newELBCalls(elbCommercial)
	rows := elbTestRows(elbCommercial)
	calls.errs[rows[TypeELBv2LoadBalancer][0]] = elbTestThrottle
	calls.errs[rows[TypeELBv2ListenerRule][5]] = elbTestBad

	_, _, err := elbScanCommercial(st, elbHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationError")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", elbTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanELBv2_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeELBv2TrustStore)
	calls := newELBCalls(elbCommercial)
	calls.errs[elbCommercial.ts("ts-tagged")] = elbTestDenied

	total, _, err := elbScanCommercial(st, elbHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"DescribeTrustStoreRevocations", "DescribeListeners", "DescribeRules", "DescribeListenerCertificates"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	// The load balancers and target groups.
	if total != 4 {
		t.Errorf("total = %d; want 4", total)
	}
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases. A load balancer not stored has no
// listeners read.
func TestScanELBv2_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op, typ, kept, lost string
		wantTotal           int
	}{
		{"DescribeTargetGroups", TypeELBv2TargetGroup, elbCommercial.tg("tg-tagged"), elbCommercial.tg("tg-bare"), elbTestTotal - 1},
		// lb-bare with its two listeners, four rules and two certificates.
		{"DescribeLoadBalancers", TypeELBv2LoadBalancer, elbCommercial.lb("lb-tagged"), elbCommercial.lb("lb-bare"), elbTestTotal - 1 - 2 - 4 - 2},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newELBCalls(elbCommercial)
			handlers := elbHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "Marker") != nil {
					return nil, elbTestBad
				}
				return list(in)
			}

			total, _, err := elbScanCommercial(st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationError")
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
			if n := len(rowsByNativeID(t, st, TypeELBv2ListenerCertificate)); n == 0 {
				t.Error("no listener certificates (the last phase) stored")
			}
		})
	}
}

// A repeated marker ends the listing as an error, keeping its pages.
func TestScanELBv2_RepeatedMarkerKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newELBCalls(elbCommercial)
	handlers := elbHandlers(calls)
	twoPages := handlers["DescribeRules"]
	handlers["DescribeRules"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*elasticloadbalancingv2.DescribeRulesOutput).NextMarker = sdkaws.String("same")
		return out, err
	}

	total, _, err := elbScanCommercial(st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != elbTestTotal {
		t.Errorf("total = %d; want %d", total, elbTestTotal)
	}
}

// One parent's failed child listing costs the other parents none of their
// rows: a parent deleted since it was listed is skipped silently, a denial
// warns once under the op, and any other error is returned.
func TestScanELBv2_PerParentListingFailures(t *testing.T) {
	f := elbCommercial
	listener := elbTestListener(f.lb("lb-tagged"), "l-bare")
	for _, c := range []struct {
		op, field, parent, typ string
		gone                   string
		lost                   int
	}{
		{"DescribeListeners", "LoadBalancerArn", f.lb("lb-bare"), TypeELBv2Listener, "LoadBalancerNotFound", 2 + 4 + 2},
		{"DescribeRules", "ListenerArn", listener, TypeELBv2ListenerRule, "ListenerNotFound", 2},
		{"DescribeListenerCertificates", "ListenerArn", listener, TypeELBv2ListenerCertificate, "ListenerNotFound", 1},
		{"DescribeTrustStoreRevocations", "TrustStoreArn", f.ts("ts-tagged"), TypeELBv2TrustStoreRevocation, "TrustStoreNotFound", 1},
	} {
		for _, e := range []struct {
			name    string
			err     error
			outcome serviceOutcome // -1: no error
			warn    bool
		}{
			{"not found", apiErr(c.gone, "gone"), -1, false},
			{"denied", elbTestDenied, -1, true},
			{"other error", elbTestBad, outcomeError, false},
		} {
			t.Run(c.op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newELBCalls(f)
				handlers := elbHandlers(calls)
				list := handlers[c.op]
				handlers[c.op] = func(in any) (any, error) {
					if sv(bacField[*string](in, c.field)) == c.parent {
						return nil, e.err
					}
					return list(in)
				}

				total, _, err := elbScanCommercial(st, handlers)
				if e.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, e.outcome)
					assertAPICode(t, err, "ValidationError")
				}
				if want := elbTestTotal - c.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				var want []string
				if e.warn {
					want = []string{"elasticloadbalancing:" + c.op}
				}
				if got := elbWarnOps(warns()); !slices.Equal(got, want) {
					t.Errorf("warnings = %v; want %v", got, want)
				}
			})
		}
	}
}

// DescribeTargetHealth: a target group it reports deleted is not stored; a
// denial stores it without targets and warns once; HealthUnavailable (a
// server error) stores it without targets and is transient; any other error
// stores it without targets and is returned.
func TestScanELBv2_TargetHealthFailures(t *testing.T) {
	tg := elbCommercial.tg("tg-tagged")
	for _, c := range []struct {
		name     string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		wantKept bool
		warn     bool
	}{
		{"not found", apiErr("TargetGroupNotFound", "gone"), -1, "", false, false},
		{"denied", elbTestDenied, -1, "", true, true},
		{"server error", apiErr("HealthUnavailable", "down"), outcomeTransient, "HealthUnavailable", true, false},
		{"other error", elbTestBad, outcomeError, "ValidationError", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newELBCalls(elbCommercial)
			handlers := elbHandlers(calls)
			health := handlers["DescribeTargetHealth"]
			handlers["DescribeTargetHealth"] = func(in any) (any, error) {
				if sv(in.(*elasticloadbalancingv2.DescribeTargetHealthInput).TargetGroupArn) == tg {
					return nil, c.err
				}
				return health(in)
			}

			total, _, err := elbScanCommercial(st, handlers)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			want := elbTestTotal
			if !c.wantKept {
				want--
			}
			if total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			r, ok := rowsByNativeID(t, st, TypeELBv2TargetGroup)[tg]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok {
				var attrs struct {
					TargetGroup elbv2types.TargetGroup
					Targets     []elbv2types.TargetDescription
				}
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.TargetGroup.TargetGroupArn) != tg || attrs.Targets != nil {
					t.Errorf("attrs = %s (%v); want the element without targets", r.AttributesJSON, err)
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"name": "tg-tagged"})
			}
			var wantWarn []string
			if c.warn {
				wantWarn = []string{"elasticloadbalancing:DescribeTargetHealth"}
			}
			if got := elbWarnOps(warns()); !slices.Equal(got, wantWarn) {
				t.Errorf("warnings = %v; want %v", got, wantWarn)
			}
		})
	}
}

// An element without the id its NativeID is built from is not stored and
// is warned about under its listing's op; its siblings are stored.
func TestScanELBv2_ElementWithoutIDWarns(t *testing.T) {
	for _, c := range []struct {
		op, typ string
		blank   func(out any)
	}{
		{"DescribeLoadBalancers", TypeELBv2LoadBalancer, func(out any) {
			out.(*elasticloadbalancingv2.DescribeLoadBalancersOutput).LoadBalancers[0].LoadBalancerArn = nil
		}},
		{"DescribeListeners", TypeELBv2Listener, func(out any) {
			out.(*elasticloadbalancingv2.DescribeListenersOutput).Listeners[0].ListenerArn = nil
		}},
		{"DescribeRules", TypeELBv2ListenerRule, func(out any) { out.(*elasticloadbalancingv2.DescribeRulesOutput).Rules[0].RuleArn = nil }},
		{"DescribeTargetGroups", TypeELBv2TargetGroup, func(out any) {
			out.(*elasticloadbalancingv2.DescribeTargetGroupsOutput).TargetGroups[0].TargetGroupArn = nil
		}},
		{"DescribeTrustStores", TypeELBv2TrustStore, func(out any) {
			out.(*elasticloadbalancingv2.DescribeTrustStoresOutput).TrustStores[0].TrustStoreArn = nil
		}},
		{"DescribeListenerCertificates", TypeELBv2ListenerCertificate, func(out any) {
			out.(*elasticloadbalancingv2.DescribeListenerCertificatesOutput).Certificates[0].CertificateArn = nil
		}},
		{"DescribeTrustStoreRevocations", TypeELBv2TrustStoreRevocation, func(out any) {
			out.(*elasticloadbalancingv2.DescribeTrustStoreRevocationsOutput).TrustStoreRevocations[0].RevocationId = nil
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newELBCalls(elbCommercial)
			handlers := elbHandlers(calls)
			list := handlers[c.op]
			blanked := false
			handlers[c.op] = func(in any) (any, error) {
				out, err := list(in)
				calls.mu.Lock()
				defer calls.mu.Unlock()
				if !blanked {
					blanked = true
					c.blank(out)
				}
				return out, err
			}

			if _, _, err := elbScanCommercial(st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := elbWarnOps(warns()); !slices.Equal(got, []string{"elasticloadbalancing:" + c.op}) {
				t.Errorf("warnings = %v; want one under %s", got, c.op)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[""]; ok {
				t.Error("a row stored under an empty NativeID")
			}
			if len(rows) == 0 {
				t.Errorf("no %s stored; the siblings must be", c.typ)
			}
			for id := range rows {
				if strings.HasSuffix(id, ":cert/") || strings.HasSuffix(id, ":rev/0") {
					t.Errorf("row %s built from an empty id", id)
				}
			}
		})
	}
}

// In GovCloud, every ARN reaches DescribeTags as listed and every row is
// stored under it.
func TestScanELBv2_TagReadsKeepPartitionARNs(t *testing.T) {
	f := elbFix{"aws-us-gov", "us-gov-west-1"}
	st := newTestStore(t)
	calls := newELBCalls(f)

	total, _, err := elbScan(context.Background(), st, partitionAccount(f.part), f, elbHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != elbTestTotal {
		t.Errorf("total = %d; want %d", total, elbTestTotal)
	}
	if got := calls.tagARNs(); !slices.Equal(got, elbTestTagARNs(f)) {
		t.Errorf("DescribeTags ARNs = %v\nwant %v", got, elbTestTagARNs(f))
	}
	for typ, ids := range elbTestRows(f) {
		if got := storedNativeIDs(t, st, typ); !slices.Equal(got, ids) {
			t.Errorf("%s rows = %v\nwant %v", typ, got, ids)
		}
	}
}

// A context ended while the scan runs is reported once, and no later phase
// starts.
func TestScanELBv2_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newELBCalls(elbCommercial)
	handlers := elbHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["DescribeTargetGroups"]
	handlers["DescribeTargetGroups"] = func(in any) (any, error) {
		if bacField[*string](in, "Marker") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := elbScan(ctx, st, newTestAccount(testAccountID), elbCommercial, handlers)
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
	for _, op := range []string{"DescribeTrustStores", "DescribeListeners", "DescribeRules"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanELBv2_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "elasticloadbalancing.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := elbHandlers(newELBCalls(elbCommercial))
		handlers["DescribeListenerCertificates"] = func(any) (any, error) { return nil, nx }
		total, _, err := elbScanCommercial(st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := elbTestTotal - 4; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newELBCalls(elbCommercial)
		calls.errs[elbCommercial.lb("lb-tagged")] = nx
		_, _, err := elbScanCommercial(st, elbHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanELBv2(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
