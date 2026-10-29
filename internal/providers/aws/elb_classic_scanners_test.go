package aws

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbtypes "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing/types"
	"github.com/icearp/disco-cli/store"
)

// elbClassicCalls records a classic scan's calls and answers DescribeTags,
// failing a call with errs of any name it is sent. The "-tagged" load
// balancers are tagged with their name.
type elbClassicCalls struct {
	mu        sync.Mutex
	pageSizes []int32
	tagCalls  [][]string
	errs      map[string]error
	names     []string // listed, one per page
}

func newELBClassicCalls(names ...string) *elbClassicCalls {
	return &elbClassicCalls{errs: map[string]error{}, names: names}
}

func (c *elbClassicCalls) handlers() map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"DescribeLoadBalancers": func(in any) (any, error) {
			req := in.(*elasticloadbalancing.DescribeLoadBalancersInput)
			c.mu.Lock()
			defer c.mu.Unlock()
			c.pageSizes = append(c.pageSizes, sdkaws.ToInt32(req.PageSize))
			i := 0
			if req.Marker != nil {
				i = len(c.pageSizes) - 1
			}
			out := &elasticloadbalancing.DescribeLoadBalancersOutput{LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{{
				LoadBalancerName: sdkaws.String(c.names[i]), CreatedTime: sdkaws.Time(elbTestCreated), VPCId: sdkaws.String("vpc-1"),
			}}}
			if i+1 < len(c.names) {
				out.NextMarker = sdkaws.String("p" + c.names[i+1])
			}
			return out, nil
		},
		"DescribeTags": func(in any) (any, error) {
			names := in.(*elasticloadbalancing.DescribeTagsInput).LoadBalancerNames
			c.mu.Lock()
			defer c.mu.Unlock()
			c.tagCalls = append(c.tagCalls, slices.Clone(names))
			for _, n := range names {
				if err := c.errs[n]; err != nil {
					return nil, err
				}
			}
			out := &elasticloadbalancing.DescribeTagsOutput{}
			for _, n := range names {
				d := elbtypes.TagDescription{LoadBalancerName: sdkaws.String(n)}
				if strings.HasSuffix(n, "-tagged") {
					d.Tags = []elbtypes.Tag{{Key: sdkaws.String("name"), Value: sdkaws.String(n)}}
				}
				out.TagDescriptions = append(out.TagDescriptions, d)
			}
			return out, nil
		},
	}
}

func elbClassicScan(st *store.Store, acct *account, region string, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct.cfg = opStubConfig(handlers, elbTestDenied)
	return scanELBClassic(context.Background(), acct, region, st, testScanID)
}

// Both pages are stored, each load balancer under the ARN built in the
// account's partition from its name, with its name, creation time, the
// element as attributes and the tags DescribeTags returns for its name. Every
// listing is sent PageSize 400. In GovCloud too.
func TestScanELBClassic_StoresTagsByName(t *testing.T) {
	for _, pf := range append([]struct{ partition, region string }{{"aws", testRegion}}, partitionFixtures...) {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newELBClassicCalls("lb-tagged", "lb-bare")
			acct := newTestAccount(testAccountID)
			if pf.partition != "aws" {
				acct = partitionAccount(pf.partition)
			}

			total, inserted, err := elbClassicScan(st, acct, pf.region, calls.handlers())
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 2 || inserted != 2 {
				t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			rows := rowsByNativeID(t, st, TypeELBClassicLoadBalancer)
			for _, name := range []string{"lb-tagged", "lb-bare"} {
				arn := "arn:" + pf.partition + ":elasticloadbalancing:" + pf.region + ":" + testAccountID + ":loadbalancer/" + name
				r, ok := rows[arn]
				if !ok {
					t.Fatalf("%s missing; rows %v", arn, rows)
				}
				var attrs elbtypes.LoadBalancerDescription
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.LoadBalancerName) != name || sv(attrs.VPCId) != "vpc-1" {
					t.Errorf("%s attrs = %s (%v); want the element", arn, r.AttributesJSON, err)
				}
				if sv(r.Name) != name || sv(r.CreatedAt) != "2026-01-02T03:04:05Z" || r.Status != nil || sv(r.Region) != pf.region {
					t.Errorf("%s Name, CreatedAt, Status, Region = %q, %q, %v, %q", arn, sv(r.Name), sv(r.CreatedAt), r.Status, sv(r.Region))
				}
				if name == "lb-tagged" {
					assertTagsMap(t, r.TagsJSON, map[string]string{"name": name})
				} else if r.TagsJSON != nil {
					t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
				}
			}
			if want := [][]string{{"lb-tagged"}, {"lb-bare"}}; !slices.EqualFunc(calls.tagCalls, want, slices.Equal) {
				t.Errorf("DescribeTags calls = %v; want %v", calls.tagCalls, want)
			}
			if !slices.Equal(calls.pageSizes, []int32{400, 400}) {
				t.Errorf("PageSize = %v; want [400 400]", calls.pageSizes)
			}
		})
	}
}

// A failed tag read: a load balancer DescribeTags reports deleted (read again
// alone after its page's call failed) is dropped silently while the others of
// its call keep their tags; a denial keeps the row untagged and warns once;
// a throttle keeps it and is transient; any other error keeps it and is an
// error.
func TestScanELBClassic_TagReadFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome // -1: no error
		wantTotal int
		warn      bool
	}{
		{"not found", apiErr("LoadBalancerNotFound", "gone"), -1, 1, false},
		{"denied", elbTestDenied, -1, 2, true},
		{"transient", elbTestThrottle, outcomeTransient, 2, false},
		{"other error", elbTestBad, outcomeError, 2, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newELBClassicCalls("lb-bad")
			calls.errs["lb-bad"] = c.err
			handlers := calls.handlers()
			handlers["DescribeLoadBalancers"] = func(any) (any, error) {
				return &elasticloadbalancing.DescribeLoadBalancersOutput{LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{
					{LoadBalancerName: sdkaws.String("lb-tagged")}, {LoadBalancerName: sdkaws.String("lb-bad")},
				}}, nil
			}

			total, _, err := elbClassicScan(st, newTestAccount(testAccountID), testRegion, handlers)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, TypeELBClassicLoadBalancer)
			prefix := "arn:aws:elasticloadbalancing:" + testRegion + ":" + testAccountID + ":loadbalancer/"
			bad, ok := rows[prefix+"lb-bad"]
			if ok != (c.wantTotal == 2) {
				t.Fatalf("lb-bad stored = %v; want %v", ok, c.wantTotal == 2)
			}
			if ok && bad.TagsJSON != nil {
				t.Errorf("lb-bad TagsJSON = %q; want nil", *bad.TagsJSON)
			}
			if c.wantTotal == 1 {
				assertTagsMap(t, rows[prefix+"lb-tagged"].TagsJSON, map[string]string{"name": "lb-tagged"})
			}
			var want []string
			if c.warn {
				want = []string{elbTagsOp}
			}
			if got := elbWarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A failed later page keeps the page stored before it; a load balancer
// without a name is not stored and is warned about.
func TestScanELBClassic_LaterPageFailureAndNamelessElement(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	handlers := newELBClassicCalls().handlers()
	handlers["DescribeLoadBalancers"] = func(in any) (any, error) {
		if in.(*elasticloadbalancing.DescribeLoadBalancersInput).Marker != nil {
			return nil, elbTestBad
		}
		return &elasticloadbalancing.DescribeLoadBalancersOutput{NextMarker: sdkaws.String("p2"), LoadBalancerDescriptions: []elbtypes.LoadBalancerDescription{
			{LoadBalancerName: sdkaws.String("lb-bare")}, {},
		}}, nil
	}

	total, _, err := elbClassicScan(st, newTestAccount(testAccountID), testRegion, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationError")
	if total != 1 {
		t.Errorf("total = %d; want 1", total)
	}
	if got := elbWarnOps(warns()); !slices.Equal(got, []string{"elasticloadbalancing:DescribeLoadBalancers"}) {
		t.Errorf("warnings = %v; want one under DescribeLoadBalancers", got)
	}
	if _, ok := rowsByNativeID(t, st, TypeELBClassicLoadBalancer)["arn:aws:elasticloadbalancing:"+testRegion+":"+testAccountID+":loadbalancer/lb-bare"]; !ok {
		t.Error("lb-bare, on the page before the failure, missing")
	}
}
