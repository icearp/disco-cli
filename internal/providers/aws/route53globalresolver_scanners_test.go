package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/route53globalresolver"
	r53grtypes "github.com/aws/aws-sdk-go-v2/service/route53globalresolver/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// r53grTestTotal is every row the fixture stores: 3 global resolvers, 2
	// access sources, 2 firewall domain lists, 3 DNS views, 2 firewall rules,
	// 2 access tokens and 2 hosted zone associations.
	r53grTestTotal = 16
	r53grTestScope = testAccountID + "/us-east-2"
	// r53grTestTagsOp is the tag op's label, spelled out so that a typo in the
	// scanner's constant fails the tests.
	r53grTestTagsOp = "route53globalresolver:ListTagsForResource"
	r53grTestPrefix = "route53globalresolver:"
)

var (
	r53grTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: route53globalresolver:List")
	r53grTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	r53grTestInvalid  = apiErr("ValidationException", "bad input")
	r53grTestGone     = apiErr("ResourceNotFoundException", "gone")
	// r53grTestTagged is the tags ListTagsForResource returns by resource
	// (an ARN's part after the account); every other ARN it is sent has none.
	r53grTestTagged = map[string]map[string]string{
		"global-resolver/gr-1":       {"team": "dns"},
		"access-source/as-1":         {"env": "prod"},
		"firewall-domain-list/fdl-1": {"list": "blocked"},
		"dns-view/v1":                {"view": "corp"},
		"access-token/at-1":          {"owner": "ops"},
	}
	// r53grTestTagReads is the resource of every ARN the fixture's scan sends
	// the tag op.
	r53grTestTagReads = []string{
		"global-resolver/gr-1", "global-resolver/gr-2", "global-resolver/gr-3",
		"access-source/as-1", "access-source/as-2",
		"firewall-domain-list/fdl-1", "firewall-domain-list/fdl-2",
		"dns-view/v1", "dns-view/v2", "dns-view/v3",
		"access-token/at-1", "access-token/at-2",
	}
)

// r53grARN is a fixture ARN in the Service Reference's format, which has no
// region: arn:{partition}:route53globalresolver::{account}:{resource}.
func r53grARN(partition, resource string) string {
	return "arn:" + partition + ":route53globalresolver::" + testAccountID + ":" + resource
}

// r53grID is the aws-partition ARN of resource.
func r53grID(resource string) string { return r53grARN("aws", resource) }

// r53grCalls records the calls of a scan: each listing's parent per call (""
// for an account-wide listing) and the ARNs sent to the tag op. errs answers
// the tag op with an error by ARN; repeat names the op whose last page
// repeats the first page's NextToken. Its fixture ARNs are in partition.
type r53grCalls struct {
	mu        sync.Mutex
	partition string
	parents   map[string][]string
	tagARNs   []string
	errs      map[string]error
	repeat    string
}

func newR53GRCalls(partition string) *r53grCalls {
	return &r53grCalls{partition: partition, parents: map[string][]string{}, errs: map[string]error{}}
}

func (c *r53grCalls) arn(resource string) *string { return ptrStr(r53grARN(c.partition, resource)) }

func (c *r53grCalls) record(op, parent string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.parents[op] = append(c.parents[op], parent)
}

func (c *r53grCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.parents[op])
}

// countFor is op's calls for parent ("" for an account-wide listing).
func (c *r53grCalls) countFor(op, parent string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, p := range c.parents[op] {
		if p == parent {
			n++
		}
	}
	return n
}

// listedFor is the distinct parents op was called for, sorted.
func (c *r53grCalls) listedFor(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	got := slices.Clone(c.parents[op])
	slices.Sort(got)
	return slices.Compact(got)
}

// sentTags is the ARNs sent to the tag op, sorted, one per call.
func (c *r53grCalls) sentTags() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	got := slices.Clone(c.tagARNs)
	slices.Sort(got)
	return got
}

// lastToken is the NextToken of op's last page: none, or the first page's
// token again when op is the one c repeats.
func (c *r53grCalls) lastToken(op string) *string {
	if c.repeat == op {
		return ptrStr("p2")
	}
	return nil
}

func (c *r53grCalls) listTags(in any) (any, error) {
	arn := sv(in.(*route53globalresolver.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err := c.errs[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	_, resource, _ := strings.Cut(arn, ":"+testAccountID+":")
	return &route53globalresolver.ListTagsForResourceOutput{Tags: r53grTestTagged[resource]}, nil
}

// r53grNoLimit fails a call that sends a MaxResults: only ListGlobalResolvers
// documents a range.
func r53grNoLimit(maxResults *int32) error {
	if maxResults != nil {
		return fmt.Errorf("MaxResults = %d; want none", *maxResults)
	}
	return nil
}

// r53grHandlers answers every op the scanner calls. Each listing holds an
// element that is not stored for want of its id (ARN, or Id for firewall
// rules and hosted zone associations), and every listing has a second page:
//   - global resolvers: gr-1 (tagged), then gr-2, one without an ARN and
//     gr-3 without an Id (stored, no DNS views listed);
//   - access sources: as-1 (tagged), then as-2 and one without an ARN;
//   - firewall domain lists: fdl-1 (tagged), then fdl-2 and one without an
//     ARN;
//   - DNS views: gr-1 has v1 (tagged), then v2 and one without an ARN; gr-2
//     has v3 without an Id (stored, no children listed);
//   - firewall rules: v1 has fr-1, then fr-2 and one without an Id; v2 has
//     none;
//   - access tokens: v1 has at-1 (tagged); v2 has at-2, then one without an
//     ARN;
//   - hosted zone associations: v1 has hz-1; v2 has hz-2, then one without
//     an Id.
func r53grHandlers(c *r53grCalls) map[string]func(in any) (any, error) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	v1, v2 := sv(c.arn("dns-view/v1")), sv(c.arn("dns-view/v2"))
	return map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListGlobalResolvers": func(in any) (any, error) {
			p := in.(*route53globalresolver.ListGlobalResolversInput)
			c.record("ListGlobalResolvers", "")
			if p.MaxResults == nil || *p.MaxResults != 100 {
				return nil, fmt.Errorf("MaxResults = %v; want 100", p.MaxResults)
			}
			if p.NextToken == nil {
				return &route53globalresolver.ListGlobalResolversOutput{NextToken: ptrStr("p2"), GlobalResolvers: []r53grtypes.GlobalResolversItem{{
					Arn: c.arn("global-resolver/gr-1"), Id: ptrStr("gr-1"), Name: ptrStr("edge"),
					Status: r53grtypes.CRResourceStatusOperational, CreatedAt: &created, DnsName: ptrStr("gr1.example"),
				}}}, nil
			}
			return &route53globalresolver.ListGlobalResolversOutput{NextToken: c.lastToken("ListGlobalResolvers"), GlobalResolvers: []r53grtypes.GlobalResolversItem{
				{Arn: c.arn("global-resolver/gr-2"), Id: ptrStr("gr-2")},
				{Id: ptrStr("gr-x"), Name: ptrStr("no-arn")},
				{Arn: c.arn("global-resolver/gr-3"), Name: ptrStr("no-id")},
			}}, nil
		},
		"ListAccessSources": func(in any) (any, error) {
			p := in.(*route53globalresolver.ListAccessSourcesInput)
			c.record("ListAccessSources", "")
			if err := r53grNoLimit(p.MaxResults); err != nil {
				return nil, err
			}
			if p.NextToken == nil {
				return &route53globalresolver.ListAccessSourcesOutput{NextToken: ptrStr("p2"), AccessSources: []r53grtypes.AccessSourcesItem{{
					Arn: c.arn("access-source/as-1"), Name: ptrStr("office"), Status: r53grtypes.CRResourceStatusOperational,
					CreatedAt: &created, Cidr: ptrStr("192.0.2.0/24"),
				}}}, nil
			}
			return &route53globalresolver.ListAccessSourcesOutput{NextToken: c.lastToken("ListAccessSources"), AccessSources: []r53grtypes.AccessSourcesItem{
				{Arn: c.arn("access-source/as-2")}, {Name: ptrStr("no-arn-source")},
			}}, nil
		},
		"ListFirewallDomainLists": func(in any) (any, error) {
			p := in.(*route53globalresolver.ListFirewallDomainListsInput)
			c.record("ListFirewallDomainLists", "")
			if err := r53grNoLimit(p.MaxResults); err != nil {
				return nil, err
			}
			if p.NextToken == nil {
				return &route53globalresolver.ListFirewallDomainListsOutput{NextToken: ptrStr("p2"), FirewallDomainLists: []r53grtypes.FirewallDomainListsItem{{
					Arn: c.arn("firewall-domain-list/fdl-1"), Name: ptrStr("blocklist"), Status: r53grtypes.CRResourceStatusCreating,
					CreatedAt: &created, Description: ptrStr("bad domains"),
				}}}, nil
			}
			return &route53globalresolver.ListFirewallDomainListsOutput{NextToken: c.lastToken("ListFirewallDomainLists"), FirewallDomainLists: []r53grtypes.FirewallDomainListsItem{
				{Arn: c.arn("firewall-domain-list/fdl-2")}, {Name: ptrStr("no-arn-list")},
			}}, nil
		},
		"ListDNSViews": func(in any) (any, error) {
			p := in.(*route53globalresolver.ListDNSViewsInput)
			resolver := sv(p.GlobalResolverId)
			c.record("ListDNSViews", resolver)
			if err := r53grNoLimit(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case resolver == "gr-1" && p.NextToken == nil:
				return &route53globalresolver.ListDNSViewsOutput{NextToken: ptrStr("p2"), DnsViews: []r53grtypes.DNSViewSummary{{
					Arn: c.arn("dns-view/v1"), Id: ptrStr("v1"), Name: ptrStr("corp"), Status: r53grtypes.ProfileResourceStatusEnabling,
					CreatedAt: &created, Description: ptrStr("corporate view"),
				}}}, nil
			case resolver == "gr-1":
				return &route53globalresolver.ListDNSViewsOutput{NextToken: c.lastToken("ListDNSViews"), DnsViews: []r53grtypes.DNSViewSummary{
					{Arn: c.arn("dns-view/v2"), Id: ptrStr("v2")}, {Id: ptrStr("v-x"), Name: ptrStr("no-arn-view")},
				}}, nil
			case resolver == "gr-2":
				return &route53globalresolver.ListDNSViewsOutput{DnsViews: []r53grtypes.DNSViewSummary{{Arn: c.arn("dns-view/v3"), Name: ptrStr("no-id-view")}}}, nil
			}
			return nil, fmt.Errorf("ListDNSViews for unknown resolver %q", resolver)
		},
		"ListFirewallRules": func(in any) (any, error) {
			p := in.(*route53globalresolver.ListFirewallRulesInput)
			view := sv(p.DnsViewId)
			c.record("ListFirewallRules", view)
			if err := r53grNoLimit(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case view == "v1" && p.NextToken == nil:
				return &route53globalresolver.ListFirewallRulesOutput{NextToken: ptrStr("p2"), FirewallRules: []r53grtypes.FirewallRulesItem{{
					Id: ptrStr("fr-1"), Name: ptrStr("block-bad"), Status: r53grtypes.CRResourceStatusOperational,
					CreatedAt: &created, FirewallDomainListId: ptrStr("fdl-1"),
				}}}, nil
			case view == "v1":
				return &route53globalresolver.ListFirewallRulesOutput{NextToken: c.lastToken("ListFirewallRules"), FirewallRules: []r53grtypes.FirewallRulesItem{
					{Id: ptrStr("fr-2")}, {Name: ptrStr("rule-without-id")},
				}}, nil
			case view == "v2":
				return &route53globalresolver.ListFirewallRulesOutput{}, nil
			}
			return nil, fmt.Errorf("ListFirewallRules for unknown view %q", view)
		},
		"ListAccessTokens": func(in any) (any, error) {
			p := in.(*route53globalresolver.ListAccessTokensInput)
			view := sv(p.DnsViewId)
			c.record("ListAccessTokens", view)
			if err := r53grNoLimit(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case view == "v1":
				return &route53globalresolver.ListAccessTokensOutput{AccessTokens: []r53grtypes.AccessTokenItem{{
					Arn: c.arn("access-token/at-1"), Name: ptrStr("client-a"), Status: r53grtypes.TokenStatusOperational,
					CreatedAt: &created, DnsViewId: ptrStr("v1"),
				}}}, nil
			case view == "v2" && p.NextToken == nil:
				return &route53globalresolver.ListAccessTokensOutput{NextToken: ptrStr("p2"), AccessTokens: []r53grtypes.AccessTokenItem{{Arn: c.arn("access-token/at-2")}}}, nil
			case view == "v2":
				return &route53globalresolver.ListAccessTokensOutput{NextToken: c.lastToken("ListAccessTokens"), AccessTokens: []r53grtypes.AccessTokenItem{{DnsViewId: ptrStr("v2")}}}, nil
			}
			return nil, fmt.Errorf("ListAccessTokens for unknown view %q", view)
		},
		"ListHostedZoneAssociations": func(in any) (any, error) {
			p := in.(*route53globalresolver.ListHostedZoneAssociationsInput)
			view := sv(p.ResourceArn)
			c.record("ListHostedZoneAssociations", view)
			if err := r53grNoLimit(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case view == v1:
				return &route53globalresolver.ListHostedZoneAssociationsOutput{HostedZoneAssociations: []r53grtypes.HostedZoneAssociationSummary{{
					Id: ptrStr("hz-1"), HostedZoneName: ptrStr("corp.example."), HostedZoneId: ptrStr("Z1"),
					Status: r53grtypes.HostedZoneAssociationStatusOperational, CreatedAt: &created,
				}}}, nil
			case view == v2 && p.NextToken == nil:
				return &route53globalresolver.ListHostedZoneAssociationsOutput{NextToken: ptrStr("p2"), HostedZoneAssociations: []r53grtypes.HostedZoneAssociationSummary{{
					Id: ptrStr("hz-2"), HostedZoneId: ptrStr("Z2"),
				}}}, nil
			case view == v2:
				return &route53globalresolver.ListHostedZoneAssociationsOutput{NextToken: c.lastToken("ListHostedZoneAssociations"), HostedZoneAssociations: []r53grtypes.HostedZoneAssociationSummary{
					{HostedZoneId: ptrStr("Z3")},
				}}, nil
			}
			return nil, fmt.Errorf("ListHostedZoneAssociations for unknown view %q", view)
		},
	}
}

// r53grScan runs the scan's real entry point for an aws account against
// handlers; every op they do not answer is denied.
func r53grScan(st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, r53grTestDenied)
	return scanRoute53GlobalResolver(context.Background(), acct, "", st, testScanID)
}

// r53grFailTokenPages is next with every call that sends a NextToken (every
// page after a listing's first) failing with err.
func r53grFailTokenPages(err error, next func(any) (any, error)) func(any) (any, error) {
	return func(in any) (any, error) {
		if v := reflect.ValueOf(in).Elem().FieldByName("NextToken"); !v.IsNil() {
			return nil, err
		}
		return next(in)
	}
}

// r53grFailParent is next with every call for parent (its GlobalResolverId,
// DnsViewId or ResourceArn) failing with err.
func r53grFailParent(parent string, err error, next func(any) (any, error)) func(any) (any, error) {
	return func(in any) (any, error) {
		v := reflect.ValueOf(in).Elem()
		for _, field := range []string{"GlobalResolverId", "DnsViewId", "ResourceArn"} {
			if f := v.FieldByName(field); f.IsValid() && sv(f.Interface().(*string)) == parent {
				return nil, err
			}
		}
		return next(in)
	}
}

func r53grAttr(t *testing.T, r store.Resource, key string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	return m[key]
}

// Every listing stores both its pages, each row under its ARN (a firewall
// rule and a hosted zone association under its built id) with its name,
// status, creation time and the listed element as attributes. The global
// resolvers, access sources, firewall domain lists, DNS views and access
// tokens carry the tags ListTagsForResource returns for their ARN, nil for a
// resource without tags, and exactly their ARNs are sent to it, once each;
// the other two types are stored untagged. Each child listing is called for
// every parent stored with the id it keys on, and a parent stored without
// one is warned about. An element without its id is not stored and warned
// about once under its listing. No error.
func TestScanRoute53GlobalResolver_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newR53GRCalls("aws")
	total, inserted, err := r53grScan(st, r53grHandlers(calls))
	if err != nil {
		t.Fatalf("scanRoute53GlobalResolver: %v", err)
	}
	if total != r53grTestTotal || inserted != r53grTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, r53grTestTotal, r53grTestTotal)
	}
	var wantTags []string
	for _, res := range r53grTestTagReads {
		wantTags = append(wantTags, r53grID(res))
	}
	slices.Sort(wantTags)
	if got := calls.sentTags(); !slices.Equal(got, wantTags) {
		t.Errorf("ARNs sent to ListTagsForResource = %v; want %v", got, wantTags)
	}

	const created = "2026-01-02T03:04:05Z"
	v1, v2 := r53grID("dns-view/v1"), r53grID("dns-view/v2")
	rows := []struct {
		typ, id, tagKey string // tagKey: the resource r53grTestTagged keys the row's tags by
		name            string
		status, created string
		attr            string
		attrWant        any
	}{
		{TypeR53GRGlobalResolver, r53grID("global-resolver/gr-1"), "global-resolver/gr-1", "edge", "OPERATIONAL", created, "DnsName", "gr1.example"},
		{TypeR53GRGlobalResolver, r53grID("global-resolver/gr-2"), "", "gr-2", "", "", "Id", "gr-2"},
		{TypeR53GRGlobalResolver, r53grID("global-resolver/gr-3"), "", "no-id", "", "", "Id", nil},
		{TypeR53GRAccessSource, r53grID("access-source/as-1"), "access-source/as-1", "office", "OPERATIONAL", created, "Cidr", "192.0.2.0/24"},
		{TypeR53GRAccessSource, r53grID("access-source/as-2"), "", r53grID("access-source/as-2"), "", "", "Arn", r53grID("access-source/as-2")},
		{TypeR53GRFirewallDomainList, r53grID("firewall-domain-list/fdl-1"), "firewall-domain-list/fdl-1", "blocklist", "CREATING", created, "Description", "bad domains"},
		{TypeR53GRFirewallDomainList, r53grID("firewall-domain-list/fdl-2"), "", r53grID("firewall-domain-list/fdl-2"), "", "", "Arn", r53grID("firewall-domain-list/fdl-2")},
		{TypeR53GRDNSView, v1, "dns-view/v1", "corp", "ENABLING", created, "Description", "corporate view"},
		{TypeR53GRDNSView, v2, "", "v2", "", "", "Id", "v2"},
		{TypeR53GRDNSView, r53grID("dns-view/v3"), "", "no-id-view", "", "", "Id", nil},
		{TypeR53GRFirewallRule, "arn:aws:route53globalresolver:us-east-2:123456789012:dns-view/v1/firewall-rule/fr-1", "", "block-bad", "OPERATIONAL", created, "FirewallDomainListId", "fdl-1"},
		{TypeR53GRFirewallRule, "arn:aws:route53globalresolver:us-east-2:123456789012:dns-view/v1/firewall-rule/fr-2", "", "fr-2", "", "", "Id", "fr-2"},
		{TypeR53GRAccessToken, r53grID("access-token/at-1"), "access-token/at-1", "client-a", "OPERATIONAL", created, "DnsViewId", "v1"},
		{TypeR53GRAccessToken, r53grID("access-token/at-2"), "", r53grID("access-token/at-2"), "", "", "Arn", r53grID("access-token/at-2")},
		{TypeR53GRHostedZoneAssociation, v1 + "/hosted-zone-association/hz-1", "", "corp.example.", "OPERATIONAL", created, "HostedZoneId", "Z1"},
		{TypeR53GRHostedZoneAssociation, v2 + "/hosted-zone-association/hz-2", "", "Z2", "", "", "Id", "hz-2"},
	}
	stored := 0
	for _, w := range rows {
		got := rowsByNativeID(t, st, w.typ)
		r, ok := got[w.id]
		if !ok {
			t.Errorf("%s %s not stored; stored %v", w.typ, w.id, nativeIDs(got))
			continue
		}
		stored++
		if r.Name == nil || *r.Name != w.name {
			t.Errorf("%s Name = %v; want %q", w.id, r.Name, w.name)
		}
		if sv(r.Status) != w.status || sv(r.CreatedAt) != w.created || sv(r.Region) != "global" {
			t.Errorf("%s status/created/region = %q/%q/%q; want %q/%q/global",
				w.id, sv(r.Status), sv(r.CreatedAt), sv(r.Region), w.status, w.created)
		}
		if w.status == "" && r.Status != nil {
			t.Errorf("%s Status = %q; want none", w.id, *r.Status)
		}
		if got := r53grAttr(t, r, w.attr); !reflect.DeepEqual(got, w.attrWant) {
			t.Errorf("%s attribute %s = %v; want %v", w.id, w.attr, got, w.attrWant)
		}
		if w.tagKey != "" {
			assertTagsMap(t, r.TagsJSON, r53grTestTagged[w.tagKey])
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", w.id, *r.TagsJSON)
		}
	}
	if stored != r53grTestTotal {
		t.Errorf("rows checked = %d; want %d", stored, r53grTestTotal)
	}

	for op, want := range map[string][]string{
		"ListGlobalResolvers":        {""},
		"ListAccessSources":          {""},
		"ListFirewallDomainLists":    {""},
		"ListDNSViews":               {"gr-1", "gr-2"},
		"ListFirewallRules":          {"v1", "v2"},
		"ListAccessTokens":           {"v1", "v2"},
		"ListHostedZoneAssociations": {v1, v2},
	} {
		if got := calls.listedFor(op); !slices.Equal(got, want) {
			t.Errorf("%s called for %v; want %v", op, got, want)
		}
	}

	wantWarns := []struct{ op, key string }{
		{"ListGlobalResolvers", "no-arn"},
		{"ListGlobalResolvers", r53grID("global-resolver/gr-3")},
		{"ListAccessSources", "no-arn-source"},
		{"ListFirewallDomainLists", "no-arn-list"},
		{"ListDNSViews", "no-arn-view of global resolver gr-1"},
		{"ListDNSViews", r53grID("dns-view/v3")},
		{"ListFirewallRules", "rule-without-id of DNS view v1"},
		{"ListAccessTokens", noIdentifier + " of DNS view v2"},
		{"ListHostedZoneAssociations", noIdentifier + " of " + v2},
	}
	w := warns()
	if len(w) != len(wantWarns) {
		t.Errorf("warnings = %v; want %d", w, len(wantWarns))
	}
	for _, want := range wantWarns {
		found := false
		for _, x := range w {
			if x.Service == r53grTestPrefix+want.op && x.Scope == r53grTestScope && strings.Contains(x.Message, want.key) {
				found = true
			}
		}
		if !found {
			t.Errorf("no warning under %s in %s naming %q; got %v", want.op, r53grTestScope, want.key, w)
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanRoute53GlobalResolver_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := r53grScan(st, r53grHandlers(newR53GRCalls("aws"))); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := r53grScan(st, r53grHandlers(newR53GRCalls("aws")))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != r53grTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, r53grTestTotal)
	}
}

// A failed tag read: ResourceNotFoundException drops the row silently, and a
// dropped parent's children are not listed; a denial keeps the row untagged
// and warns once under the tag op; a throttle keeps it and is transient; any
// other error keeps it and is an error.
func TestScanRoute53GlobalResolver_TagReadFailures(t *testing.T) {
	kinds := []struct {
		typ, resource string
		// childOp is a listing whose parent the row is, sent parent;
		// droppedTotal is the total once the row and its children are gone.
		childOp, parent string
		droppedTotal    int
	}{
		{TypeR53GRGlobalResolver, "global-resolver/gr-2", "ListDNSViews", "gr-2", r53grTestTotal - 2},
		{TypeR53GRAccessSource, "access-source/as-2", "", "", r53grTestTotal - 1},
		{TypeR53GRFirewallDomainList, "firewall-domain-list/fdl-2", "", "", r53grTestTotal - 1},
		// v2's access token and hosted zone association go with it.
		{TypeR53GRDNSView, "dns-view/v2", "ListAccessTokens", "v2", r53grTestTotal - 3},
		{TypeR53GRAccessToken, "access-token/at-2", "", "", r53grTestTotal - 1},
	}
	for _, k := range kinds {
		cases := []struct {
			name      string
			err       error
			outcome   serviceOutcome // -1: no error
			code      string
			wantTotal int
			wantKept  bool
			wantWarns int
		}{
			{"not found", r53grTestGone, -1, "", k.droppedTotal, false, 0},
			{"denied", r53grTestDenied, -1, "", r53grTestTotal, true, 1},
			{"transient", r53grTestThrottle, outcomeTransient, "ThrottlingException", r53grTestTotal, true, 0},
			{"other error", r53grTestInvalid, outcomeError, "ValidationException", r53grTestTotal, true, 0},
		}
		for _, c := range cases {
			t.Run(k.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := bacWarnings(st, r53grTestTagsOp)
				calls := newR53GRCalls("aws")
				arn := r53grID(k.resource)
				calls.errs[arn] = c.err
				total, _, err := r53grScan(st, r53grHandlers(calls))
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
				r, ok := rowsByNativeID(t, st, k.typ)[arn]
				if ok != c.wantKept {
					t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				if k.childOp != "" && slices.Contains(calls.listedFor(k.childOp), k.parent) != c.wantKept {
					t.Errorf("%s called for %s: %v; want %v", k.childOp, k.parent, !c.wantKept, c.wantKept)
				}
				if len(*warns) != c.wantWarns {
					t.Fatalf("%s warnings = %v; want %d", r53grTestTagsOp, *warns, c.wantWarns)
				}
				for _, x := range *warns {
					if x.Scope != r53grTestScope || !strings.Contains(x.Message, arn) {
						t.Errorf("warning %+v; want one in %s naming %s", x, r53grTestScope, arn)
					}
				}
			})
		}
	}
}

// Denied tag reads in every tagged listing send one warning for the whole
// scan, under the tag op, naming the first denied row; the rows are kept
// untagged.
func TestScanRoute53GlobalResolver_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := bacWarnings(st, r53grTestTagsOp)
	calls := newR53GRCalls("aws")
	denied := []struct{ typ, resource string }{
		{TypeR53GRGlobalResolver, "global-resolver/gr-1"},
		{TypeR53GRAccessSource, "access-source/as-1"},
		{TypeR53GRFirewallDomainList, "firewall-domain-list/fdl-1"},
		{TypeR53GRDNSView, "dns-view/v1"},
		{TypeR53GRAccessToken, "access-token/at-1"},
	}
	for _, d := range denied {
		calls.errs[r53grID(d.resource)] = r53grTestDenied
	}
	total, _, err := r53grScan(st, r53grHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != r53grTestTotal {
		t.Errorf("total = %d; want %d", total, r53grTestTotal)
	}
	first := r53grID("global-resolver/gr-1")
	if len(*warns) != 1 || (*warns)[0].Scope != r53grTestScope || !strings.Contains((*warns)[0].Message, first) {
		t.Fatalf("%s warnings = %v; want 1 in %s naming %s", r53grTestTagsOp, *warns, r53grTestScope, first)
	}
	for _, d := range denied {
		if r, ok := rowsByNativeID(t, st, d.typ)[r53grID(d.resource)]; !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v; want stored untagged", d.resource, ok)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanRoute53GlobalResolver_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, r53grTestTagsOp)
	calls := newR53GRCalls("aws")
	calls.errs[r53grID("dns-view/v1")] = r53grTestThrottle
	calls.errs[r53grID("access-source/as-2")] = r53grTestInvalid
	_, _, err := r53grScan(st, r53grHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", r53grTestTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs, and no warning is sent
// about rows the write lost; the phases before it keep their rows. A listing
// writes each page before it lists the next, so the failed write also ends
// the listing of its two-page parent after one call; another parent's
// listing may be called before the failure stops the phase.
func TestScanRoute53GlobalResolver_FailedWriteStopsScan(t *testing.T) {
	v2 := r53grID("dns-view/v2")
	cases := []struct {
		typ, op string
		// firstRow is a row whose tag read is denied, so a warning would
		// follow a write that succeeded; "" for an untagged type.
		firstRow string
		// paged is the parent ("" for an account-wide listing) whose listing
		// has two pages.
		paged     string
		later     []string
		wantTotal int // the rows of the phases before it
	}{
		{TypeR53GRGlobalResolver, "ListGlobalResolvers", "global-resolver/gr-1", "", []string{
			"ListAccessSources", "ListFirewallDomainLists", "ListDNSViews", "ListFirewallRules", "ListAccessTokens", "ListHostedZoneAssociations",
		}, 0},
		// 3 resolvers.
		{TypeR53GRAccessSource, "ListAccessSources", "access-source/as-1", "", []string{
			"ListFirewallDomainLists", "ListDNSViews", "ListFirewallRules", "ListAccessTokens", "ListHostedZoneAssociations",
		}, 3},
		// 3 resolvers and 2 access sources.
		{TypeR53GRFirewallDomainList, "ListFirewallDomainLists", "firewall-domain-list/fdl-1", "", []string{
			"ListDNSViews", "ListFirewallRules", "ListAccessTokens", "ListHostedZoneAssociations",
		}, 5},
		// 3 resolvers, 2 access sources and 2 domain lists.
		{TypeR53GRDNSView, "ListDNSViews", "dns-view/v1", "gr-1", []string{
			"ListFirewallRules", "ListAccessTokens", "ListHostedZoneAssociations",
		}, 7},
		// And 3 DNS views.
		{TypeR53GRFirewallRule, "ListFirewallRules", "", "v1", []string{
			"ListAccessTokens", "ListHostedZoneAssociations",
		}, 10},
		// And 2 firewall rules.
		{TypeR53GRAccessToken, "ListAccessTokens", "access-token/at-1", "v2", []string{"ListHostedZoneAssociations"}, 12},
		// And 2 access tokens.
		{TypeR53GRHostedZoneAssociation, "ListHostedZoneAssociations", "", v2, nil, 14},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			tagWarns := countOpWarnings(st, r53grTestTagsOp)
			opWarns := countOpWarnings(st, r53grTestPrefix+c.op)
			refuseStoreWrites(t, st, c.typ)
			calls := newR53GRCalls("aws")
			if c.firstRow != "" {
				calls.errs[r53grID(c.firstRow)] = r53grTestDenied
			}
			total, _, err := r53grScan(st, r53grHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *tagWarns != 0 || *opWarns != 0 {
				t.Errorf("tag, %s warnings = %d, %d; want 0, 0", c.op, *tagWarns, *opWarns)
			}
			if n := calls.countFor(c.op, c.paged); n > 1 {
				t.Errorf("%s called %d times for %q; want at most 1, none after the failed write", c.op, n, c.paged)
			}
			for _, op := range c.later {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times after the failed write; want 0", op, n)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// An element without an ARN is warned about under the name it has, else its
// Id, else as an element with no identifier.
func TestScanRoute53GlobalResolver_NoARNNamedByID(t *testing.T) {
	cases := []struct {
		op  string
		out any
		key string
	}{
		{"ListGlobalResolvers", &route53globalresolver.ListGlobalResolversOutput{GlobalResolvers: []r53grtypes.GlobalResolversItem{{Id: ptrStr("gr-9")}}}, "gr-9"},
		{"ListAccessSources", &route53globalresolver.ListAccessSourcesOutput{AccessSources: []r53grtypes.AccessSourcesItem{{Id: ptrStr("as-9")}}}, "as-9"},
		{"ListFirewallDomainLists", &route53globalresolver.ListFirewallDomainListsOutput{FirewallDomainLists: []r53grtypes.FirewallDomainListsItem{{Id: ptrStr("fdl-9")}}}, "fdl-9"},
		{"ListDNSViews", &route53globalresolver.ListDNSViewsOutput{DnsViews: []r53grtypes.DNSViewSummary{{Id: ptrStr("v-9")}}}, "v-9 of global resolver gr-1"},
		{"ListAccessTokens", &route53globalresolver.ListAccessTokensOutput{AccessTokens: []r53grtypes.AccessTokenItem{{Id: ptrStr("at-9")}}}, "at-9 of DNS view v1"},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, r53grTestPrefix+c.op)
			h := r53grHandlers(newR53GRCalls("aws"))
			h[c.op] = func(any) (any, error) { return c.out, nil }
			if _, _, err := r53grScan(st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warns) != 1 || !strings.Contains((*warns)[0].Message, c.key) {
				t.Errorf("%s warnings = %v; want 1 naming %q", c.op, *warns, c.key)
			}
		})
	}
}

// r53grTestOps is every listing the scan makes, in phase order.
var r53grTestOps = []string{
	"ListGlobalResolvers", "ListAccessSources", "ListFirewallDomainLists", "ListDNSViews",
	"ListFirewallRules", "ListAccessTokens", "ListHostedZoneAssociations",
}

// A listing's failed later page keeps the pages read before it, and its
// sibling parents' rows; its error does not stop the later phases.
func TestScanRoute53GlobalResolver_LaterPageFailureKeepsPages(t *testing.T) {
	cases := []struct {
		op        string
		wantTotal int // the rows of the failed page, and of their children, are lost
	}{
		// gr-2 and gr-3, and gr-2's v3.
		{"ListGlobalResolvers", r53grTestTotal - 3},
		{"ListAccessSources", r53grTestTotal - 1},
		{"ListFirewallDomainLists", r53grTestTotal - 1},
		// v2, its access token and its hosted zone association.
		{"ListDNSViews", r53grTestTotal - 3},
		{"ListFirewallRules", r53grTestTotal - 1},
		{"ListAccessTokens", r53grTestTotal},
		{"ListHostedZoneAssociations", r53grTestTotal},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newR53GRCalls("aws")
			h := r53grHandlers(calls)
			h[c.op] = r53grFailTokenPages(r53grTestInvalid, h[c.op])
			total, _, err := r53grScan(st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if n := calls.count("ListHostedZoneAssociations"); n == 0 {
				t.Error("ListHostedZoneAssociations (the last phase) not called")
			}
		})
	}
}

// A listing whose NextToken repeats ends as an error, keeping both pages.
func TestScanRoute53GlobalResolver_RepeatedTokenKeepsPages(t *testing.T) {
	for _, op := range r53grTestOps {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newR53GRCalls("aws")
			calls.repeat = op
			total, _, err := r53grScan(st, r53grHandlers(calls))
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != r53grTestTotal {
				t.Errorf("total = %d; want %d", total, r53grTestTotal)
			}
		})
	}
}

// A failure of one parent's child listing costs its siblings none of their
// rows: a parent the op reports gone is skipped silently; a denied one warns
// once under the op; any other error, a transient one included, is returned
// after the siblings' rows are stored, since the parent's children are lost.
func TestScanRoute53GlobalResolver_ChildListingFailures(t *testing.T) {
	parents := []struct {
		op, parent string
		// wantTotal: the parent's rows, and their children, are lost.
		wantTotal int
		// notStored is the op's warning about an element without its id that
		// the failed parent no longer lists.
		notStored int
	}{
		// v1, v2 and both pages of children under them: 2 rules, 2 tokens, 2
		// hosted zone associations. gr-2's v3 is still stored without an Id.
		{"ListDNSViews", "gr-1", r53grTestTotal - 8, 1},
		{"ListFirewallRules", "v1", r53grTestTotal - 2, 0},
		{"ListAccessTokens", "v2", r53grTestTotal - 1, 0},
		{"ListHostedZoneAssociations", r53grID("dns-view/v1"), r53grTestTotal - 1, 1},
	}
	for _, p := range parents {
		cases := []struct {
			name      string
			err       error
			outcome   serviceOutcome // -1: no error
			wantWarns int
		}{
			{"gone", r53grTestGone, -1, 0},
			{"denied", r53grTestDenied, -1, 1},
			{"other error", r53grTestInvalid, outcomeError, 0},
			{"transient", io.EOF, outcomeTransient, 0},
		}
		for _, c := range cases {
			t.Run(p.op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, r53grTestPrefix+p.op)
				h := r53grHandlers(newR53GRCalls("aws"))
				h[p.op] = r53grFailParent(p.parent, c.err, h[p.op])
				total, _, err := r53grScan(st, h)
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
				}
				if total != p.wantTotal {
					t.Errorf("total = %d; want %d", total, p.wantTotal)
				}
				if *warns != c.wantWarns+p.notStored {
					t.Errorf("warnings = %d; want %d", *warns, c.wantWarns+p.notStored)
				}
			})
		}
	}
}

// A listing denied after its first page keeps that page and its sibling
// parents' rows, and warns once.
func TestScanRoute53GlobalResolver_LaterPageDeniedKeepsPages(t *testing.T) {
	cases := []struct {
		op        string
		wantTotal int // the rows of the denied page, and of their children, are lost
		// wantWarns is the denial's warning, plus the warnings about elements
		// without their id that a page still read holds.
		wantWarns int
	}{
		{"ListGlobalResolvers", r53grTestTotal - 3, 1},
		{"ListAccessSources", r53grTestTotal - 1, 1},
		{"ListFirewallDomainLists", r53grTestTotal - 1, 1},
		// gr-2's v3 is still stored without an Id.
		{"ListDNSViews", r53grTestTotal - 3, 2},
		{"ListFirewallRules", r53grTestTotal - 1, 1},
		{"ListAccessTokens", r53grTestTotal, 1},
		{"ListHostedZoneAssociations", r53grTestTotal, 1},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, r53grTestPrefix+c.op)
			h := r53grHandlers(newR53GRCalls("aws"))
			h[c.op] = r53grFailTokenPages(r53grTestDenied, h[c.op])
			total, _, err := r53grScan(st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if *warns != c.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, c.wantWarns)
			}
		})
	}
}

// A listing that fails outright does not stop the later phases; its child
// phases have no parents and make no call.
func TestScanRoute53GlobalResolver_FailedListingRunsLaterPhases(t *testing.T) {
	st := newTestStore(t)
	calls := newR53GRCalls("aws")
	h := r53grHandlers(calls)
	h["ListGlobalResolvers"] = func(any) (any, error) { return nil, r53grTestInvalid }
	total, _, err := r53grScan(st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	// Only the 2 access sources and 2 firewall domain lists remain.
	if total != 4 {
		t.Errorf("total = %d; want 4", total)
	}
	for _, op := range []string{"ListDNSViews", "ListFirewallRules", "ListAccessTokens", "ListHostedZoneAssociations"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeR53GRFirewallDomainList)); n != 2 {
		t.Errorf("firewall domain list rows (a later phase) = %d; want 2", n)
	}
}

// A phase's transient error beside another phase's hard error is demoted to
// one warning under its own op, so the hard error decides the outcome. The
// warnings are compared with a scan whose phase fails with a hard error
// instead, so the demotion must add exactly one warning, under the op that
// failed, and none under any other.
func TestScanRoute53GlobalResolver_TransientPhaseDemotedBesideHardError(t *testing.T) {
	scan := func(t *testing.T, op, hardOp string, opErr error) map[string]int {
		t.Helper()
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		h := r53grHandlers(newR53GRCalls("aws"))
		h[op] = func(any) (any, error) { return nil, opErr }
		h[hardOp] = func(any) (any, error) { return nil, r53grTestInvalid }
		_, _, err := r53grScan(st, h)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "ValidationException")
		got := map[string]int{}
		for _, w := range warns() {
			got[w.Service]++
		}
		return got
	}
	for _, op := range r53grTestOps {
		t.Run(op, func(t *testing.T) {
			hardOp := "ListAccessSources"
			if op == hardOp {
				hardOp = "ListFirewallDomainLists"
			}
			base := scan(t, op, hardOp, r53grTestInvalid)
			got := scan(t, op, hardOp, io.EOF)
			label := r53grTestPrefix + op
			if got[label] != base[label]+1 {
				t.Errorf("%s warnings = %d; want %d", label, got[label], base[label]+1)
			}
			for svc, n := range got {
				if svc != label && n != base[svc] {
					t.Errorf("%s warnings = %d; want %d", svc, n, base[svc])
				}
			}
		})
	}
}

// An account with nothing in any account-wide listing stores nothing, reads
// no tags, makes no child listing and warns about nothing.
func TestScanRoute53GlobalResolver_EmptyAccount(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newR53GRCalls("aws")
	h := r53grHandlers(calls)
	h["ListGlobalResolvers"] = func(any) (any, error) { return &route53globalresolver.ListGlobalResolversOutput{}, nil }
	h["ListAccessSources"] = func(any) (any, error) { return &route53globalresolver.ListAccessSourcesOutput{}, nil }
	h["ListFirewallDomainLists"] = func(any) (any, error) { return &route53globalresolver.ListFirewallDomainListsOutput{}, nil }
	total, inserted, err := r53grScan(st, h)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if got := calls.sentTags(); len(got) != 0 {
		t.Errorf("ARNs sent to ListTagsForResource = %v; want none", got)
	}
	for _, op := range []string{"ListDNSViews", "ListFirewallRules", "ListAccessTokens", "ListHostedZoneAssociations"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// An NXDOMAIN with nothing stored is a region without the service; once rows
// were stored, an NXDOMAIN from a later listing or a tag read is a failure
// of this scan, and the stored rows keep their counts.
func TestScanRoute53GlobalResolver_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "route53globalresolver.us-east-2.api.aws", IsNotFound: true}
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanRoute53GlobalResolver(context.Background(), acct, "", st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
	t.Run("later listing", func(t *testing.T) {
		st := newTestStore(t)
		h := r53grHandlers(newR53GRCalls("aws"))
		h["ListHostedZoneAssociations"] = func(any) (any, error) { return nil, nx }
		total, _, err := r53grScan(st, h)
		assertGlueOutcome(t, err, outcomeError)
		if total != r53grTestTotal-2 {
			t.Errorf("total = %d; want %d", total, r53grTestTotal-2)
		}
	})
	t.Run("tag read", func(t *testing.T) {
		st := newTestStore(t)
		calls := newR53GRCalls("aws")
		calls.errs[r53grID("global-resolver/gr-1")] = nx
		total, _, err := r53grScan(st, r53grHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != r53grTestTotal {
			t.Errorf("total = %d; want %d", total, r53grTestTotal)
		}
	})
}

// In an aws-us-gov account the ARNs the listings return reach the tag op
// unchanged, and the built firewall rule id carries the account's partition
// and the region it was read in.
func TestScanRoute53GlobalResolver_GovPartition(t *testing.T) {
	const partition, region = "aws-us-gov", "us-gov-west-1"
	st := newTestStore(t)
	calls := newR53GRCalls(partition)
	acct := partitionAccount(partition)
	client := route53globalresolver.NewFromConfig(opStubConfig(r53grHandlers(calls), r53grTestDenied))
	total, _, err := scanR53GRWithClient(context.Background(), client, acct, region, st, testScanID)
	if err != nil || total != r53grTestTotal {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, r53grTestTotal)
	}
	var want []string
	for _, res := range r53grTestTagReads {
		want = append(want, r53grARN(partition, res))
	}
	slices.Sort(want)
	if got := calls.sentTags(); !slices.Equal(got, want) {
		t.Errorf("ARNs sent to ListTagsForResource = %v; want %v", got, want)
	}
	rule := "arn:aws-us-gov:route53globalresolver:us-gov-west-1:123456789012:dns-view/v1/firewall-rule/fr-1"
	if _, ok := rowsByNativeID(t, st, TypeR53GRFirewallRule)[rule]; !ok {
		t.Errorf("firewall rule %s not stored; stored %v", rule, nativeIDs(rowsByNativeID(t, st, TypeR53GRFirewallRule)))
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeR53GRGlobalResolver)[r53grARN(partition, "global-resolver/gr-1")].TagsJSON, r53grTestTagged["global-resolver/gr-1"])
}
