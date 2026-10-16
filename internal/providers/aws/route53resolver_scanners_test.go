package aws

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53resolver"
	r53rtypes "github.com/aws/aws-sdk-go-v2/service/route53resolver/types"
	"github.com/icearp/disco-cli/store"
)

var (
	r53rDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: route53resolver:List")
	r53rGone       = apiErr("ResourceNotFoundException", "gone")
	r53rValidation = apiErr("ValidationException", "not ready")
	r53rThrottle   = apiErr("ThrottlingException", "slow down")
	r53rBadInput   = apiErr("InvalidRequestException", "bad")
)

const (
	r53rTestPrefix = "arn:aws:route53resolver:us-east-1:123456789012:"

	r53rGroupA  = r53rTestPrefix + "firewall-rule-group/rslvr-frg-a"
	r53rGroupB  = r53rTestPrefix + "firewall-rule-group/rslvr-frg-b"
	r53rGroupC  = r53rTestPrefix + "firewall-rule-group/rslvr-frg-c"
	r53rRuleA1  = r53rGroupA + "/firewall-rule/domain-list/rslvr-fdl-1"
	r53rRuleA1Q = r53rGroupA + "/firewall-rule/domain-list/rslvr-fdl-1/qtype/AAAA"
	r53rRuleB   = r53rGroupB + "/firewall-rule/threat-protection/rslvr-ftp-1"
	r53rRuleC   = r53rGroupC + "/firewall-rule/domain-list/rslvr-fdl-1"

	r53rEndpointA = r53rTestPrefix + "resolver-endpoint/rslvr-in-a"
	r53rEndpointB = r53rTestPrefix + "resolver-endpoint/rslvr-in-b"
	r53rEndpointC = r53rTestPrefix + "resolver-endpoint/rslvr-in-c"
	r53rIPA1      = r53rEndpointA + "/ip-address/rni-a1"
	r53rIPA2      = r53rEndpointA + "/ip-address/rni-a2"
	r53rIPB       = r53rEndpointB + "/ip-address/rni-b1"
	r53rIPC       = r53rEndpointC + "/ip-address/rni-c1"

	r53rList1 = r53rTestPrefix + "firewall-domain-list/rslvr-fdl-1"
)

func r53rTestClient(handlers map[string]func(in any) (any, error)) *route53resolver.Client {
	return route53resolver.NewFromConfig(opStubConfig(handlers, r53rDenied))
}

// r53rTestParents seeds a parent row of typ per id and returns them as
// fan-out parents, each ARN being prefix + id.
func r53rTestParents(t *testing.T, st *store.Store, typ, prefix string, ids ...string) []childParent {
	t.Helper()
	parents := make([]childParent, 0, len(ids))
	arns := make([]string, 0, len(ids))
	for _, id := range ids {
		parents = append(parents, childParent{id: id, arn: prefix + id})
		arns = append(arns, prefix+id)
	}
	seedParentRows(t, st, typ, arns...)
	return parents
}

func TestR53RFirewallRuleNativeID(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule r53rtypes.FirewallRule
		want string
	}{
		{"domain list", r53rtypes.FirewallRule{FirewallDomainListId: ptrStr("rslvr-fdl-1")}, r53rRuleA1},
		{"domain list and qtype", r53rtypes.FirewallRule{FirewallDomainListId: ptrStr("rslvr-fdl-1"), Qtype: ptrStr("AAAA")}, r53rRuleA1Q},
		{"threat protection", r53rtypes.FirewallRule{FirewallThreatProtectionId: ptrStr("rslvr-ftp-1")}, r53rGroupA + "/firewall-rule/threat-protection/rslvr-ftp-1"},
		{"neither", r53rtypes.FirewallRule{Qtype: ptrStr("A"), Priority: sdkaws.Int32(1)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := r53rFirewallRuleNativeID(r53rGroupA, &tc.rule); got != tc.want {
				t.Errorf("native id = %q, want %q", got, tc.want)
			}
		})
	}
}

func r53rRuleA1Element() r53rtypes.FirewallRule {
	return r53rtypes.FirewallRule{
		FirewallDomainListId: ptrStr("rslvr-fdl-1"), Name: ptrStr("block-bad"), Status: ptrStr("COMPLETE"),
		CreationTime: ptrStr("2026-01-02T03:04:05Z"), Priority: sdkaws.Int32(100), Action: r53rtypes.ActionBlock,
	}
}

func r53rIPA1Element() r53rtypes.IpAddressResponse {
	return r53rtypes.IpAddressResponse{
		IpId: ptrStr("rni-a1"), Ip: ptrStr("10.0.0.5"), SubnetId: ptrStr("subnet-a"),
		Status: r53rtypes.IpAddressStatusAttached, CreationTime: ptrStr("2026-01-02T03:04:05Z"),
	}
}

// r53rRuleHandler serves rslvr-frg-a in two pages (a domain-list rule; then
// the same list for AAAA queries), and any
// other group one threat-protection rule. A group id in fail returns that
// error. Every call's group id is recorded, and a page size other than the
// model cap fails the test.
func r53rRuleHandler(t *testing.T, calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*route53resolver.ListFirewallRulesInput)
		id := sv(req.FirewallRuleGroupId)
		calls.add(id)
		if got := sdkaws.ToInt32(req.MaxResults); got != 100 {
			t.Errorf("ListFirewallRules(%s) MaxResults = %d, want 100", id, got)
		}
		if err := fail[id]; err != nil {
			return nil, err
		}
		if id != "rslvr-frg-a" {
			return &route53resolver.ListFirewallRulesOutput{FirewallRules: []r53rtypes.FirewallRule{
				{FirewallThreatProtectionId: ptrStr("rslvr-ftp-1"), Name: ptrStr("threats")},
			}}, nil
		}
		if req.NextToken == nil {
			return &route53resolver.ListFirewallRulesOutput{FirewallRules: []r53rtypes.FirewallRule{r53rRuleA1Element()}, NextToken: ptrStr("p2")}, nil
		}
		return &route53resolver.ListFirewallRulesOutput{FirewallRules: []r53rtypes.FirewallRule{
			{FirewallDomainListId: ptrStr("rslvr-fdl-1"), Qtype: ptrStr("AAAA"), Name: ptrStr("block-bad-v6")},
		}}, nil
	}
}

func scanR53RTestRules(t *testing.T, st *store.Store, handler func(in any) (any, error), ids ...string) (int, error) {
	t.Helper()
	parents := r53rTestParents(t, st, TypeRoute53ResolverFirewallRuleGroup, r53rTestPrefix+"firewall-rule-group/", ids...)
	client := r53rTestClient(map[string]func(any) (any, error){"ListFirewallRules": handler})
	total, _, err := scanR53RFirewallRules(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	return total, err
}

func TestScanR53RFirewallRules_StoresEveryPageOfEveryGroup(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanR53RTestRules(t, st, r53rRuleHandler(t, &calls, nil), "rslvr-frg-a", "rslvr-frg-b")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"rslvr-frg-a", "rslvr-frg-b"}) {
		t.Errorf("groups listed = %v, want [rslvr-frg-a rslvr-frg-b]", got)
	}
	rows := rowsByNativeID(t, st, TypeRoute53ResolverFirewallRule)
	ids := slices.Sorted(maps.Keys(rows))
	want := []string{r53rRuleA1, r53rRuleA1Q, r53rRuleB}
	if total != 3 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 3, %v", total, ids, want)
	}
	r := rows[r53rRuleA1]
	if sv(r.Name) != "block-bad" || sv(r.Status) != "COMPLETE" || sv(r.CreatedAt) != "2026-01-02T03:04:05Z" || sv(r.Region) != testRegion {
		t.Errorf("name/status/created/region = %q/%q/%q/%q", sv(r.Name), sv(r.Status), sv(r.CreatedAt), sv(r.Region))
	}
	if want := mustJSON(r53rRuleA1Element()); r.AttributesJSON != want {
		t.Errorf("attrs = %s\nwant    %s", r.AttributesJSON, want)
	}
	if s := rows[r53rRuleB].Status; s != nil {
		t.Errorf("status of a rule without one = %q, want unset", *s)
	}
	assertContainsNative(t, st, r53rGroupA, r53rRuleA1)
	assertContainsNative(t, st, r53rGroupA, r53rRuleA1Q)
	assertContainsNative(t, st, r53rGroupB, r53rRuleB)
	assertContainsEdgesDeclared(t, st)
}

func TestScanR53RFirewallRules_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanR53RTestRules(t, st, func(any) (any, error) { return &route53resolver.ListFirewallRulesOutput{}, nil }, "rslvr-frg-a")
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeRoute53ResolverFirewallRule)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// Rules that carry a FirewallRuleType but neither a domain list nor a threat
// protection id are not stored; one warning counts them across groups and
// names the first's group, name and priority. Keyed siblings are stored.
func TestScanR53RFirewallRules_UnkeyedRulesWarnOnce(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	unkeyed := func(name string, priority int32) r53rtypes.FirewallRule {
		return r53rtypes.FirewallRule{
			Name: ptrStr(name), Priority: sdkaws.Int32(priority),
			FirewallRuleType: &r53rtypes.FirewallRuleType{PartnerThreatProtection: &r53rtypes.PartnerThreatProtectionConfig{}},
		}
	}
	handler := func(in any) (any, error) {
		switch id := sv(in.(*route53resolver.ListFirewallRulesInput).FirewallRuleGroupId); id {
		case "rslvr-frg-a":
			return &route53resolver.ListFirewallRulesOutput{FirewallRules: []r53rtypes.FirewallRule{r53rRuleA1Element(), unkeyed("partner", 300)}}, nil
		default:
			return &route53resolver.ListFirewallRulesOutput{FirewallRules: []r53rtypes.FirewallRule{unkeyed("partner-b", 400)}}, nil
		}
	}
	// One group keeps "first" deterministic; the two-group run below checks
	// the count spans groups.
	total, err := scanR53RTestRules(t, st, handler, "rslvr-frg-a")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	ids := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeRoute53ResolverFirewallRule)))
	if total != 1 || !slices.Equal(ids, []string{r53rRuleA1}) {
		t.Errorf("total = %d, rows = %v; want 1, [%s]", total, ids, r53rRuleA1)
	}
	if len(warnings) != 1 || warnings[0].Service != r53rListFirewallRulesOp {
		t.Fatalf("warnings = %+v, want one for %s", warnings, r53rListFirewallRulesOp)
	}
	for _, want := range []string{"1 firewall rule(s)", "rslvr-frg-a", `"partner"`, "priority 300"} {
		if !strings.Contains(warnings[0].Message, want) {
			t.Errorf("warning %q does not name %s", warnings[0].Message, want)
		}
	}

	st2 := newTestStore(t)
	var warnings2 []store.ScanWarning
	st2.OnWarn = func(w store.ScanWarning) { warnings2 = append(warnings2, w) }
	if _, err := scanR53RTestRules(t, st2, handler, "rslvr-frg-a", "rslvr-frg-b"); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(warnings2) != 1 || !strings.Contains(warnings2[0].Message, "2 firewall rule(s)") {
		t.Errorf("warnings across two groups = %+v, want one counting 2", warnings2)
	}
}

// A group deleted after it was listed is skipped silently. A denial, a
// ValidationException and a transient error each warn once for the op and
// keep the other groups; any other error fails the phase.
func TestScanR53RFirewallRules_FailedGroup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      error
		wantWarns int
	}{
		{"gone", r53rGone, 0},
		{"denied", r53rDenied, 1},
		{"not ready", r53rValidation, 1},
		{"transient", r53rThrottle, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, r53rListFirewallRulesOp)
			var calls parentCalls
			fail := map[string]error{"rslvr-frg-b": tc.fail, "rslvr-frg-c": tc.fail}
			total, err := scanR53RTestRules(t, st, r53rRuleHandler(t, &calls, fail), "rslvr-frg-a", "rslvr-frg-b", "rslvr-frg-c")
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			ids := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeRoute53ResolverFirewallRule)))
			if want := []string{r53rRuleA1, r53rRuleA1Q}; total != 2 || *warns != tc.wantWarns || !slices.Equal(ids, want) {
				t.Errorf("total = %d, warnings = %d, rows = %v; want 2, %d, %v", total, *warns, ids, tc.wantWarns, want)
			}
		})
	}
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		_, err := scanR53RTestRules(t, st, r53rRuleHandler(t, &calls, map[string]error{"rslvr-frg-b": r53rBadInput}), "rslvr-frg-a", "rslvr-frg-b")
		assertAPICode(t, err, "InvalidRequestException")
	})
}

// r53rIPHandler serves rslvr-in-a in two pages (an IPv4 address; then an
// IPv6-only address plus an element without an IpId), and any other endpoint
// one address. An endpoint id in fail returns that error.
func r53rIPHandler(t *testing.T, calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*route53resolver.ListResolverEndpointIpAddressesInput)
		id := sv(req.ResolverEndpointId)
		calls.add(id)
		if got := sdkaws.ToInt32(req.MaxResults); got != 100 {
			t.Errorf("ListResolverEndpointIpAddresses(%s) MaxResults = %d, want 100", id, got)
		}
		if err := fail[id]; err != nil {
			return nil, err
		}
		if id != "rslvr-in-a" {
			suffix := id[len("rslvr-in-"):]
			return &route53resolver.ListResolverEndpointIpAddressesOutput{IpAddresses: []r53rtypes.IpAddressResponse{
				{IpId: ptrStr("rni-" + suffix + "1"), Ip: ptrStr("10.0.1.5"), SubnetId: ptrStr("subnet-" + suffix)},
			}}, nil
		}
		if req.NextToken == nil {
			return &route53resolver.ListResolverEndpointIpAddressesOutput{IpAddresses: []r53rtypes.IpAddressResponse{r53rIPA1Element()}, NextToken: ptrStr("p2")}, nil
		}
		return &route53resolver.ListResolverEndpointIpAddressesOutput{IpAddresses: []r53rtypes.IpAddressResponse{
			{IpId: ptrStr("rni-a2"), Ipv6: ptrStr("2001:db8::5"), SubnetId: ptrStr("subnet-a")},
			{Ip: ptrStr("10.0.0.9"), SubnetId: ptrStr("subnet-a")},
		}}, nil
	}
}

func scanR53RTestIPs(t *testing.T, st *store.Store, handler func(in any) (any, error), ids ...string) (int, error) {
	t.Helper()
	parents := r53rTestParents(t, st, TypeRoute53ResolverResolverEndpoint, r53rTestPrefix+"resolver-endpoint/", ids...)
	client := r53rTestClient(map[string]func(any) (any, error){"ListResolverEndpointIpAddresses": handler})
	total, _, err := scanR53RResolverEndpointIPAddresses(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	return total, err
}

func TestScanR53RResolverEndpointIPAddresses_StoresEveryPageOfEveryEndpoint(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanR53RTestIPs(t, st, r53rIPHandler(t, &calls, nil), "rslvr-in-a", "rslvr-in-b")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"rslvr-in-a", "rslvr-in-b"}) {
		t.Errorf("endpoints listed = %v, want [rslvr-in-a rslvr-in-b]", got)
	}
	rows := rowsByNativeID(t, st, TypeRoute53ResolverResolverEndpointIPAddress)
	ids := slices.Sorted(maps.Keys(rows))
	want := []string{r53rIPA1, r53rIPA2, r53rIPB}
	if total != 3 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 3, %v", total, ids, want)
	}
	a1 := rows[r53rIPA1]
	if sv(a1.Name) != "10.0.0.5" || sv(a1.Status) != "ATTACHED" || sv(a1.CreatedAt) != "2026-01-02T03:04:05Z" || sv(a1.Region) != testRegion {
		t.Errorf("name/status/created/region = %q/%q/%q/%q", sv(a1.Name), sv(a1.Status), sv(a1.CreatedAt), sv(a1.Region))
	}
	if want := mustJSON(r53rIPA1Element()); a1.AttributesJSON != want {
		t.Errorf("attrs = %s\nwant    %s", a1.AttributesJSON, want)
	}
	if a2 := rows[r53rIPA2]; sv(a2.Name) != "2001:db8::5" || a2.Status != nil {
		t.Errorf("IPv6-only name/status = %q/%v, want 2001:db8::5/unset", sv(a2.Name), a2.Status)
	}
	assertContainsNative(t, st, r53rEndpointA, r53rIPA1)
	assertContainsNative(t, st, r53rEndpointA, r53rIPA2)
	assertContainsNative(t, st, r53rEndpointB, r53rIPB)
	assertContainsEdgesDeclared(t, st)
}

func TestScanR53RResolverEndpointIPAddresses_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanR53RTestIPs(t, st, func(any) (any, error) { return &route53resolver.ListResolverEndpointIpAddressesOutput{}, nil }, "rslvr-in-a")
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeRoute53ResolverResolverEndpointIPAddress)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// An endpoint deleted after it was listed is skipped silently. A denial and a
// transient error each warn once for the op and keep the other endpoints. The
// op models no ValidationException, so like any other error it fails the
// phase.
func TestScanR53RResolverEndpointIPAddresses_FailedEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      error
		wantWarns int
	}{
		{"gone", r53rGone, 0},
		{"denied", r53rDenied, 1},
		{"transient", r53rThrottle, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, r53rListEndpointIPAddressesOp)
			var calls parentCalls
			fail := map[string]error{"rslvr-in-b": tc.fail, "rslvr-in-c": tc.fail}
			total, err := scanR53RTestIPs(t, st, r53rIPHandler(t, &calls, fail), "rslvr-in-a", "rslvr-in-b", "rslvr-in-c")
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			ids := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeRoute53ResolverResolverEndpointIPAddress)))
			if want := []string{r53rIPA1, r53rIPA2}; total != 2 || *warns != tc.wantWarns || !slices.Equal(ids, want) {
				t.Errorf("total = %d, warnings = %d, rows = %v; want 2, %d, %v", total, *warns, ids, tc.wantWarns, want)
			}
		})
	}
	for _, fail := range []error{r53rValidation, r53rBadInput} {
		t.Run("other "+fail.Error(), func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			_, err := scanR53RTestIPs(t, st, r53rIPHandler(t, &calls, map[string]error{"rslvr-in-b": fail}), "rslvr-in-a", "rslvr-in-b")
			if err == nil {
				t.Fatal("err = nil, want the endpoint's error")
			}
		})
	}
}

// r53rTopLevelHandlers answers scanRoute53Resolver with: rule groups
// rslvr-frg-a (owned) and rslvr-frg-c (shared with this account), endpoint
// rslvr-in-a, firewall config fc-1 (the last region-wide phase), one rule per
// group, one IP address per endpoint and domain list rslvr-fdl-1. Every other
// region-wide op is denied.
func r53rTopLevelHandlers(calls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListFirewallRuleGroups": func(any) (any, error) {
			return &route53resolver.ListFirewallRuleGroupsOutput{FirewallRuleGroups: []r53rtypes.FirewallRuleGroupMetadata{
				{Arn: ptrStr(r53rGroupA), Id: ptrStr("rslvr-frg-a"), ShareStatus: r53rtypes.ShareStatusSharedByMe},
				{Arn: ptrStr(r53rGroupC), Id: ptrStr("rslvr-frg-c"), ShareStatus: r53rtypes.ShareStatusSharedWithMe},
			}}, nil
		},
		"ListResolverEndpoints": func(any) (any, error) {
			return &route53resolver.ListResolverEndpointsOutput{ResolverEndpoints: []r53rtypes.ResolverEndpoint{
				{Arn: ptrStr(r53rEndpointA), Id: ptrStr("rslvr-in-a")},
			}}, nil
		},
		"ListFirewallConfigs": func(any) (any, error) {
			return &route53resolver.ListFirewallConfigsOutput{FirewallConfigs: []r53rtypes.FirewallConfig{{Id: ptrStr("fc-1")}}}, nil
		},
		"ListFirewallRules": func(in any) (any, error) {
			id := sv(in.(*route53resolver.ListFirewallRulesInput).FirewallRuleGroupId)
			calls.add(id)
			return &route53resolver.ListFirewallRulesOutput{FirewallRules: []r53rtypes.FirewallRule{{FirewallDomainListId: ptrStr("rslvr-fdl-1")}}}, nil
		},
		"ListResolverEndpointIpAddresses": func(any) (any, error) {
			return &route53resolver.ListResolverEndpointIpAddressesOutput{IpAddresses: []r53rtypes.IpAddressResponse{{IpId: ptrStr("rni-a1")}}}, nil
		},
		"ListFirewallDomainLists": func(any) (any, error) {
			return &route53resolver.ListFirewallDomainListsOutput{FirewallDomainLists: []r53rtypes.FirewallDomainListMetadata{
				{Arn: ptrStr(r53rList1), Id: ptrStr("rslvr-fdl-1"), Name: ptrStr("blocked")},
			}}, nil
		},
	}
}

var r53rFirewallConfigFC1 = r53rARN(testRegion, testAccountID, "firewall-config", "fc-1")

func scanR53RTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, r53rDenied)
	total, _, err := scanRoute53Resolver(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanRoute53Resolver_StoresNewRows(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanR53RTopLevel(t, st, r53rTopLevelHandlers(&calls))
	if err != nil {
		t.Fatalf("scanRoute53Resolver: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"rslvr-frg-a"}) {
		t.Errorf("groups whose rules were listed = %v, want only the owned rslvr-frg-a", got)
	}
	// 2 rule groups, 1 endpoint, 1 firewall config, 1 rule, 1 IP address, 1 domain list.
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	assertContainsNative(t, st, r53rGroupA, r53rRuleA1)
	assertContainsNative(t, st, r53rEndpointA, r53rIPA1)
	assertContainsEdgesDeclared(t, st)
}

// Each new phase runs after every region-wide phase, and a new phase's hard
// error reaches the caller without costing the other new phases their rows.
func TestScanRoute53Resolver_NewPhaseErrorKeepsOtherRows(t *testing.T) {
	for _, tc := range []struct {
		op       string
		lostType string
	}{
		{"ListFirewallRules", TypeRoute53ResolverFirewallRule},
		{"ListResolverEndpointIpAddresses", TypeRoute53ResolverResolverEndpointIPAddress},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			handlers := r53rTopLevelHandlers(&calls)
			handlers[tc.op] = func(any) (any, error) { return nil, r53rBadInput }
			total, err := scanR53RTopLevel(t, st, handlers)
			assertAPICode(t, err, "InvalidRequestException")
			if _, ok := rowsByNativeID(t, st, TypeRoute53ResolverFirewallConfig)[r53rFirewallConfigFC1]; !ok {
				t.Errorf("firewall config of the last region-wide phase not stored")
			}
			for _, typ := range []string{TypeRoute53ResolverFirewallRule, TypeRoute53ResolverResolverEndpointIPAddress} {
				want := 1
				if typ == tc.lostType {
					want = 0
				}
				if n := len(rowsByNativeID(t, st, typ)); n != want {
					t.Errorf("%s rows = %d, want %d", typ, n, want)
				}
			}
			// 1 domain list, 2 rule groups, 1 endpoint, 1 firewall config, and
			// the 1 row of the new phase that did not fail.
			if total != 6 {
				t.Errorf("total = %d, want 6", total)
			}
		})
	}
}

// A hard error in the region-wide phases is returned, and the new phases
// still run over the parents stored before it.
func TestScanRoute53Resolver_RegionPhaseErrorStillRunsNewPhases(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	handlers := r53rTopLevelHandlers(&calls)
	handlers["ListFirewallConfigs"] = func(any) (any, error) { return nil, r53rBadInput }
	total, err := scanR53RTopLevel(t, st, handlers)
	assertAPICode(t, err, "InvalidRequestException")
	for _, typ := range []string{TypeRoute53ResolverFirewallRule, TypeRoute53ResolverResolverEndpointIPAddress} {
		if n := len(rowsByNativeID(t, st, typ)); n != 1 {
			t.Errorf("%s rows = %d, want 1", typ, n)
		}
	}
	// 1 domain list, 2 rule groups, 1 endpoint, 1 rule, 1 IP address.
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
}

// A transient error in the region-wide phases is demoted to one warning so
// that a new phase's hard error still reaches the dispatcher as a hard error.
func TestScanRoute53Resolver_TransientDoesNotMaskHardError(t *testing.T) {
	for _, failed := range []string{"ListFirewallRules", "ListResolverEndpointIpAddresses"} {
		t.Run(failed, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "route53resolver")
			var calls parentCalls
			handlers := r53rTopLevelHandlers(&calls)
			handlers["ListFirewallConfigs"] = func(any) (any, error) { return nil, r53rThrottle }
			handlers[failed] = func(any) (any, error) { return nil, r53rBadInput }
			_, err := scanR53RTopLevel(t, st, handlers)
			assertAPICode(t, err, "InvalidRequestException")
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v, want outcomeError", got)
			}
			if *warns != 1 {
				t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
			}
		})
	}
}
