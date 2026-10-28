package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
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

const (
	// r53rListedCount is the rows the region-wide listings store: two per
	// listing, one per page.
	r53rListedCount = 24
	// r53rTotalCount adds one firewall rule per rule group and one IP
	// address per endpoint.
	r53rTotalCount = r53rListedCount + 4
	// r53rOtherAccountPrefix is the ARN prefix of a resource another account
	// shares with this one.
	r53rOtherAccountPrefix = "arn:aws:route53resolver:us-east-1:999999999999:"
)

// r53rKind is one region-wide listing of the fixture: its op, the type it
// stores, the ids of its two rows (idBase "-a" on the first page, "-b" on
// the second), whether ListTagsForResource reads their tags, the NativeID
// and Name a row is stored under, and page, which builds the op's output
// holding the one element of id with next.
type r53rKind struct {
	op, typ, idBase string
	tagged          bool
	nativeID        func(id string) string
	name            func(id string) string
	page            func(k r53rKind, id string, next *string) any
}

func (k r53rKind) id(page string) string { return k.idBase + "-" + page }

func (k r53rKind) arn(page string) string { return k.nativeID(k.id(page)) }

func r53rKinds() []r53rKind {
	s := sdkaws.String
	named := func(id string) string { return "name-" + id }
	plain := func(id string) string { return id }
	under := func(kind string) func(string) string {
		return func(id string) string { return r53rTestPrefix + kind + "/" + id }
	}
	return []r53rKind{
		{"ListFirewallDomainLists", TypeRoute53ResolverFirewallDomainList, "rslvr-fdl", true, under("firewall-domain-list"), named, func(k r53rKind, id string, next *string) any {
			return &route53resolver.ListFirewallDomainListsOutput{FirewallDomainLists: []r53rtypes.FirewallDomainListMetadata{{Arn: s(k.nativeID(id)), Id: s(id), Name: s(named(id))}}, NextToken: next}
		}},
		{"ListFirewallRuleGroups", TypeRoute53ResolverFirewallRuleGroup, "rslvr-frg", true, under("firewall-rule-group"), named, func(k r53rKind, id string, next *string) any {
			return &route53resolver.ListFirewallRuleGroupsOutput{FirewallRuleGroups: []r53rtypes.FirewallRuleGroupMetadata{{
				Arn: s(k.nativeID(id)), Id: s(id), Name: s(named(id)), OwnerId: s(testAccountID), ShareStatus: r53rtypes.ShareStatusNotShared,
			}}, NextToken: next}
		}},
		{"ListFirewallRuleGroupAssociations", TypeRoute53ResolverFirewallRuleGroupAssociation, "rslvr-frgassoc", true, under("firewall-rule-group-association"), plain, func(k r53rKind, id string, next *string) any {
			return &route53resolver.ListFirewallRuleGroupAssociationsOutput{FirewallRuleGroupAssociations: []r53rtypes.FirewallRuleGroupAssociation{{Arn: s(k.nativeID(id)), Id: s(id), Name: s(named(id))}}, NextToken: next}
		}},
		{"ListOutpostResolvers", TypeRoute53ResolverOutpostResolver, "rslvr-op", true, under("outpost-resolver"), named, func(k r53rKind, id string, next *string) any {
			return &route53resolver.ListOutpostResolversOutput{OutpostResolvers: []r53rtypes.OutpostResolver{{Arn: s(k.nativeID(id)), Id: s(id), Name: s(named(id))}}, NextToken: next}
		}},
		{"ListResolverConfigs", TypeRoute53ResolverResolverConfig, "vpc-rc", false, under("resolver-config"), plain, func(_ r53rKind, id string, next *string) any {
			return &route53resolver.ListResolverConfigsOutput{ResolverConfigs: []r53rtypes.ResolverConfig{{Id: s("rslvr-rc-" + id), ResourceId: s(id)}}, NextToken: next}
		}},
		{"ListResolverDnssecConfigs", TypeRoute53ResolverResolverDNSSECConfig, "vpc-ds", false, under("resolver-dnssec-config"), plain, func(_ r53rKind, id string, next *string) any {
			return &route53resolver.ListResolverDnssecConfigsOutput{ResolverDnssecConfigs: []r53rtypes.ResolverDnssecConfig{{Id: s("rslvr-ds-" + id), ResourceId: s(id)}}, NextToken: next}
		}},
		{"ListResolverEndpoints", TypeRoute53ResolverResolverEndpoint, "rslvr-in", true, under("resolver-endpoint"), named, func(k r53rKind, id string, next *string) any {
			return &route53resolver.ListResolverEndpointsOutput{ResolverEndpoints: []r53rtypes.ResolverEndpoint{{Arn: s(k.nativeID(id)), Id: s(id), Name: s(named(id))}}, NextToken: next}
		}},
		{"ListResolverQueryLogConfigs", TypeRoute53ResolverResolverQueryLoggingConfig, "rslvr-rqlc", true, under("resolver-query-log-config"), named, func(k r53rKind, id string, next *string) any {
			return &route53resolver.ListResolverQueryLogConfigsOutput{ResolverQueryLogConfigs: []r53rtypes.ResolverQueryLogConfig{{Arn: s(k.nativeID(id)), Id: s(id), Name: s(named(id))}}, NextToken: next}
		}},
		{"ListResolverQueryLogConfigAssociations", TypeRoute53ResolverResolverQueryLoggingConfigAssociation, "rslvr-qlcassoc", false, under("resolver-query-log-config-association"), plain, func(_ r53rKind, id string, next *string) any {
			return &route53resolver.ListResolverQueryLogConfigAssociationsOutput{ResolverQueryLogConfigAssociations: []r53rtypes.ResolverQueryLogConfigAssociation{{Id: s(id)}}, NextToken: next}
		}},
		{"ListResolverRules", TypeRoute53ResolverResolverRule, "rslvr-rr", true, under("resolver-rule"), named, func(k r53rKind, id string, next *string) any {
			return &route53resolver.ListResolverRulesOutput{ResolverRules: []r53rtypes.ResolverRule{{Arn: s(k.nativeID(id)), Id: s(id), Name: s(named(id))}}, NextToken: next}
		}},
		{"ListResolverRuleAssociations", TypeRoute53ResolverResolverRuleAssociation, "rslvr-rrassoc", false, under("resolver-rule-association"), named, func(_ r53rKind, id string, next *string) any {
			return &route53resolver.ListResolverRuleAssociationsOutput{ResolverRuleAssociations: []r53rtypes.ResolverRuleAssociation{{Id: s(id), Name: s(named(id))}}, NextToken: next}
		}},
		{"ListFirewallConfigs", TypeRoute53ResolverFirewallConfig, "rslvr-fc", false, under("firewall-config"), plain, func(_ r53rKind, id string, next *string) any {
			return &route53resolver.ListFirewallConfigsOutput{FirewallConfigs: []r53rtypes.FirewallConfig{{Id: s(id), ResourceId: s("vpc-" + id)}}, NextToken: next}
		}},
	}
}

func r53rKindOf(t *testing.T, op string) r53rKind {
	t.Helper()
	for _, k := range r53rKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind for %s", op)
	return r53rKind{}
}

// r53rTaggedARNs is the ARN of every row the fixture's tag reads are sent.
func r53rTaggedARNs() []string {
	var arns []string
	for _, k := range r53rKinds() {
		if k.tagged {
			arns = append(arns, k.arn("a"), k.arn("b"))
		}
	}
	return arns
}

// r53rCalls records the stubbed calls: how often each op ran, the
// MaxResults each was sent, the rule groups and endpoints whose children
// were listed, and the ARNs sent to ListTagsForResource, which it answers
// from tags (in two pages for an ARN in twoPages) or errs, all keyed by ARN.
// Tag reads run concurrently.
type r53rCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	maxResults map[string][]int32
	groups     parentCalls
	endpoints  parentCalls
	tagARNs    []string
	tags       map[string]map[string]string
	twoPages   map[string]bool
	errs       map[string]error
}

// newR53RCalls tags every row of the first pages with its type.
func newR53RCalls() *r53rCalls {
	c := &r53rCalls{ops: map[string]int{}, maxResults: map[string][]int32{}, tags: map[string]map[string]string{}, twoPages: map[string]bool{}, errs: map[string]error{}}
	for _, k := range r53rKinds() {
		if k.tagged {
			c.tags[k.arn("a")] = map[string]string{"type": k.typ}
		}
	}
	return c
}

func (c *r53rCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.maxResults[op] = append(c.maxResults[op], sdkaws.ToInt32(bacField[*int32](in, "MaxResults")))
}

func (c *r53rCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *r53rCalls) sentTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *r53rCalls) listTags(in any) (any, error) {
	req := in.(*route53resolver.ListTagsForResourceInput)
	arn := sv(req.ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	if req.NextToken == nil {
		c.tagARNs = append(c.tagARNs, arn)
	}
	err, tags, paged := c.errs[arn], c.tags[arn], c.twoPages[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []r53rtypes.Tag
	for _, k := range slices.Sorted(maps.Keys(tags)) {
		list = append(list, r53rtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(tags[k])})
	}
	if !paged {
		return &route53resolver.ListTagsForResourceOutput{Tags: list}, nil
	}
	switch sv(req.NextToken) {
	case "":
		return &route53resolver.ListTagsForResourceOutput{Tags: list[:1], NextToken: sdkaws.String("tags-2")}, nil
	case "tags-2":
		return &route53resolver.ListTagsForResourceOutput{Tags: list[1:]}, nil
	}
	return nil, r53rBadInput
}

// pages answers k's op with row "a" on the first page and "b" on the
// second.
func (c *r53rCalls) pages(k r53rKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		if bacField[*string](in, "NextToken") == nil {
			return k.page(k, k.id("a"), sdkaws.String("p2")), nil
		}
		return k.page(k, k.id("b"), nil), nil
	}
}

// r53rHandlers answers every op the scan calls: each region-wide listing
// in two pages (r53rCalls.pages), one domain-list rule per rule group and
// one IP address per endpoint, recorded in c.groups and c.endpoints.
func r53rHandlers(c *r53rCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListFirewallRules": func(in any) (any, error) {
			c.record("ListFirewallRules", in)
			c.groups.add(sv(in.(*route53resolver.ListFirewallRulesInput).FirewallRuleGroupId))
			return &route53resolver.ListFirewallRulesOutput{FirewallRules: []r53rtypes.FirewallRule{{FirewallDomainListId: sdkaws.String("rslvr-fdl-1")}}}, nil
		},
		"ListResolverEndpointIpAddresses": func(in any) (any, error) {
			c.record("ListResolverEndpointIpAddresses", in)
			c.endpoints.add(sv(in.(*route53resolver.ListResolverEndpointIpAddressesInput).ResolverEndpointId))
			return &route53resolver.ListResolverEndpointIpAddressesOutput{IpAddresses: []r53rtypes.IpAddressResponse{{IpId: sdkaws.String("rni-1")}}}, nil
		},
	}
	for _, k := range r53rKinds() {
		h[k.op] = c.pages(k)
	}
	return h
}

// r53rScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func r53rScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, r53rDenied)
	return scanRoute53Resolver(ctx, acct, testRegion, st, testScanID)
}

func r53rRuleOf(groupARN string) string { return groupARN + "/firewall-rule/domain-list/rslvr-fdl-1" }

func r53rIPOf(endpointARN string) string { return endpointARN + "/ip-address/rni-1" }

// Every listing stores both its pages, each row under its NativeID and named
// by its name or id, with the tags ListTagsForResource returns for its Arn
// and nil for a resource without tags; the types the op does not take are
// stored without tags. Exactly the Arn of every row of a tagged type is sent
// to the tag op. Every call is sent the model's page cap: 100, and 10 for
// ListFirewallConfigs. The rules and IP addresses of every rule group and
// endpoint are listed and contained. No error or warning.
func TestScanRoute53Resolver_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newR53RCalls()

	total, inserted, err := r53rScan(context.Background(), st, r53rHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != r53rTotalCount || inserted != r53rTotalCount {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, r53rTotalCount, r53rTotalCount)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, k := range r53rKinds() {
		for _, page := range []string{"a", "b"} {
			nativeID := k.arn(page)
			r, ok := rowsByNativeID(t, st, k.typ)[nativeID]
			if !ok {
				t.Errorf("%s %s missing", k.typ, nativeID)
				continue
			}
			if want := k.name(k.id(page)); sv(r.Name) != want || sv(r.Region) != testRegion {
				t.Errorf("%s Name, Region = %q, %q; want %q, %q", nativeID, sv(r.Name), sv(r.Region), want, testRegion)
			}
			if !strings.Contains(r.AttributesJSON, `"`+k.id(page)+`"`) {
				t.Errorf("%s attrs = %s; want the listed element", nativeID, r.AttributesJSON)
			}
			if k.tagged && page == "a" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", nativeID, *r.TagsJSON)
			}
		}
	}
	for op, sent := range calls.maxResults {
		want := int32(100)
		if op == "ListFirewallConfigs" {
			want = 10
		}
		for _, n := range sent {
			if n != want {
				t.Errorf("%s MaxResults = %d; want %d", op, n, want)
			}
		}
	}
	if got, want := calls.sentTagARNs(), slices.Sorted(slices.Values(r53rTaggedARNs())); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	groups, endpoints := r53rKindOf(t, "ListFirewallRuleGroups"), r53rKindOf(t, "ListResolverEndpoints")
	for _, page := range []string{"a", "b"} {
		assertContainsNative(t, st, groups.arn(page), r53rRuleOf(groups.arn(page)))
		assertContainsNative(t, st, endpoints.arn(page), r53rIPOf(endpoints.arn(page)))
	}
	assertContainsEdgesDeclared(t, st)
}

// A rescan stores the same rows without inserting any.
func TestScanRoute53Resolver_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := r53rScan(context.Background(), st, r53rHandlers(newR53RCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := r53rScan(context.Background(), st, r53rHandlers(newR53RCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != r53rTotalCount || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, r53rTotalCount)
	}
}

// Tags returned over two pages are stored together, the second page asked
// for with the first page's NextToken; a NextToken that repeats keeps the
// row untagged and fails the scan.
func TestScanRoute53Resolver_TagPages(t *testing.T) {
	k := r53rKindOf(t, "ListOutpostResolvers")
	arn := k.arn("a")
	t.Run("two pages", func(t *testing.T) {
		st := newTestStore(t)
		calls := newR53RCalls()
		calls.tags[arn] = map[string]string{"env": "prod", "team": "dns"}
		calls.twoPages[arn] = true
		if _, _, err := r53rScan(context.Background(), st, r53rHandlers(calls)); err != nil {
			t.Fatalf("scan: %v", err)
		}
		assertTagsMap(t, rowsByNativeID(t, st, k.typ)[arn].TagsJSON, map[string]string{"env": "prod", "team": "dns"})
	})
	t.Run("repeated token", func(t *testing.T) {
		st := newTestStore(t)
		calls := newR53RCalls()
		handlers := r53rHandlers(calls)
		handlers["ListTagsForResource"] = func(in any) (any, error) {
			if sv(in.(*route53resolver.ListTagsForResourceInput).ResourceArn) == arn {
				return &route53resolver.ListTagsForResourceOutput{NextToken: sdkaws.String("same")}, nil
			}
			return calls.listTags(in)
		}
		total, _, err := r53rScan(context.Background(), st, handlers)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		if total != r53rTotalCount {
			t.Errorf("total = %d; want %d", total, r53rTotalCount)
		}
		r, ok := rowsByNativeID(t, st, k.typ)[arn]
		if !ok || r.TagsJSON != nil {
			t.Errorf("row stored = %v, TagsJSON = %v; want stored untagged", ok, r.TagsJSON)
		}
	})
}

// A failed tag read: ResourceNotFoundException, the op's modeled not-found,
// drops the row silently, and a rule group or endpoint dropped is no parent
// of the child phase; a denial keeps the row untagged and warns once under
// route53resolver:ListTagsForResource; a throttle keeps it and is a
// transient error; InvalidRequestException keeps it and is a hard error.
func TestScanRoute53Resolver_TagReadFailures(t *testing.T) {
	for _, op := range []string{"ListFirewallDomainLists", "ListFirewallRuleGroups", "ListResolverEndpoints", "ListResolverRules"} {
		k := r53rKindOf(t, op)
		arn := k.arn("a")
		children := 0
		if op == "ListFirewallRuleGroups" || op == "ListResolverEndpoints" {
			children = 1
		}
		t.Run(k.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantTotal int
				wantKept  bool
				wantWarns int
			}{
				{"not found", r53rGone, -1, "", r53rTotalCount - 1 - children, false, 0},
				{"denied", r53rDenied, -1, "", r53rTotalCount, true, 1},
				{"transient", r53rThrottle, outcomeTransient, "ThrottlingException", r53rTotalCount, true, 0},
				{"other error", r53rBadInput, outcomeError, "InvalidRequestException", r53rTotalCount, true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, r53rTagsOp)
					calls := newR53RCalls()
					calls.errs[arn] = c.err

					total, _, err := r53rScan(context.Background(), st, r53rHandlers(calls))
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
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", r53rTagsOp, *warnings, *all, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
					wantParents := []string{k.id("a"), k.id("b")}
					if !c.wantKept {
						wantParents = wantParents[1:]
					}
					switch op {
					case "ListFirewallRuleGroups":
						if got := calls.groups.distinct(); !slices.Equal(got, wantParents) {
							t.Errorf("rule groups whose rules were listed = %v; want %v", got, wantParents)
						}
					case "ListResolverEndpoints":
						if got := calls.endpoints.distinct(); !slices.Equal(got, wantParents) {
							t.Errorf("endpoints whose IP addresses were listed = %v; want %v", got, wantParents)
						}
					}
				})
			}
		})
	}
}

// Denied tag reads in several phases send one warning for the scan, never
// one per row or per phase.
func TestScanRoute53Resolver_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	all := countWarnings(st)
	warnings := bacWarnings(st, r53rTagsOp)
	calls := newR53RCalls()
	denied := []string{
		r53rKindOf(t, "ListFirewallDomainLists").arn("a"),
		r53rKindOf(t, "ListFirewallRuleGroups").arn("b"),
		r53rKindOf(t, "ListResolverRules").arn("a"),
	}
	for _, arn := range denied {
		calls.errs[arn] = r53rDenied
	}

	total, _, err := r53rScan(context.Background(), st, r53rHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != r53rTotalCount {
		t.Errorf("total = %d; want %d", total, r53rTotalCount)
	}
	if len(*warnings) != 1 || *all != 1 {
		t.Fatalf("%s warnings = %v (all ops %d); want 1", r53rTagsOp, *warnings, *all)
	}
	for _, k := range r53rKinds() {
		for _, arn := range []string{k.arn("a"), k.arn("b")} {
			if !slices.Contains(denied, arn) {
				continue
			}
			if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored = %v, TagsJSON = %v; want stored untagged", arn, ok, r.TagsJSON)
			}
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanRoute53Resolver_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, r53rTagsOp)
	calls := newR53RCalls()
	calls.errs[r53rKindOf(t, "ListOutpostResolvers").arn("a")] = r53rThrottle
	calls.errs[r53rKindOf(t, "ListResolverQueryLogConfigs").arn("b")] = r53rBadInput

	_, _, err := r53rScan(context.Background(), st, r53rHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", r53rTagsOp, *warns)
	}
}

// An AWS-default domain list (ManagedOwnerName set) or Resolver rule
// (autodefined) is stored as managed and its tags are not read.
func TestScanRoute53Resolver_ManagedRowsNotTagRead(t *testing.T) {
	st := newTestStore(t)
	calls := newR53RCalls()
	handlers := r53rHandlers(calls)
	list := r53rTestPrefix + "firewall-domain-list/rslvr-fdl-aws"
	rule := r53rTestPrefix + "resolver-rule/rslvr-autodefined-rr-internet-resolver"
	calls.errs[list] = r53rBadInput
	calls.errs[rule] = r53rBadInput
	handlers["ListFirewallDomainLists"] = func(in any) (any, error) {
		calls.record("ListFirewallDomainLists", in)
		return &route53resolver.ListFirewallDomainListsOutput{FirewallDomainLists: []r53rtypes.FirewallDomainListMetadata{{
			Arn: &list, Id: sdkaws.String("rslvr-fdl-aws"), ManagedOwnerName: sdkaws.String("Route 53 Resolver DNS Firewall"),
		}}}, nil
	}
	handlers["ListResolverRules"] = func(in any) (any, error) {
		calls.record("ListResolverRules", in)
		return &route53resolver.ListResolverRulesOutput{ResolverRules: []r53rtypes.ResolverRule{{
			Arn: &rule, Id: sdkaws.String("rslvr-autodefined-rr-internet-resolver"),
		}}}, nil
	}

	if _, _, err := r53rScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for typ, arn := range map[string]string{TypeRoute53ResolverFirewallDomainList: list, TypeRoute53ResolverResolverRule: rule} {
		r, ok := rowsByNativeID(t, st, typ)[arn]
		if !ok || !r.ManagedByProvider || r.TagsJSON != nil {
			t.Errorf("%s stored = %v, managed = %v, TagsJSON = %v; want stored managed untagged", arn, ok, r.ManagedByProvider, r.TagsJSON)
		}
		if slices.Contains(calls.sentTagARNs(), arn) {
			t.Errorf("tags of %s read", arn)
		}
	}
}

// A rule group, query logging config or Resolver rule another account
// shares with this one is stored untagged and silently when the tag read is
// denied or finds no such resource; a shared rule group is no rule parent.
func TestScanRoute53Resolver_SharedRowsKeptUntagged(t *testing.T) {
	for _, refusal := range []error{r53rDenied, r53rGone} {
		t.Run(refusal.Error(), func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			calls := newR53RCalls()
			handlers := r53rHandlers(calls)
			group := r53rOtherAccountPrefix + "firewall-rule-group/rslvr-frg-shared"
			config := r53rOtherAccountPrefix + "resolver-query-log-config/rslvr-rqlc-shared"
			rule := r53rOtherAccountPrefix + "resolver-rule/rslvr-rr-shared"
			for _, arn := range []string{group, config, rule} {
				calls.errs[arn] = refusal
			}
			shared := r53rtypes.ShareStatusSharedWithMe
			other := sdkaws.String("999999999999")
			handlers["ListFirewallRuleGroups"] = func(in any) (any, error) {
				calls.record("ListFirewallRuleGroups", in)
				return &route53resolver.ListFirewallRuleGroupsOutput{FirewallRuleGroups: []r53rtypes.FirewallRuleGroupMetadata{{
					Arn: &group, Id: sdkaws.String("rslvr-frg-shared"), OwnerId: other, ShareStatus: shared,
				}}}, nil
			}
			handlers["ListResolverQueryLogConfigs"] = func(in any) (any, error) {
				calls.record("ListResolverQueryLogConfigs", in)
				return &route53resolver.ListResolverQueryLogConfigsOutput{ResolverQueryLogConfigs: []r53rtypes.ResolverQueryLogConfig{{
					Arn: &config, Id: sdkaws.String("rslvr-rqlc-shared"), OwnerId: other, ShareStatus: shared,
				}}}, nil
			}
			handlers["ListResolverRules"] = func(in any) (any, error) {
				calls.record("ListResolverRules", in)
				return &route53resolver.ListResolverRulesOutput{ResolverRules: []r53rtypes.ResolverRule{{
					Arn: &rule, Id: sdkaws.String("rslvr-rr-shared"), OwnerId: other, ShareStatus: shared,
				}}}, nil
			}

			if _, _, err := r53rScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			for typ, arn := range map[string]string{
				TypeRoute53ResolverFirewallRuleGroup:          group,
				TypeRoute53ResolverResolverQueryLoggingConfig: config,
				TypeRoute53ResolverResolverRule:               rule,
			} {
				r, ok := rowsByNativeID(t, st, typ)[arn]
				if !ok || r.TagsJSON != nil {
					t.Errorf("%s stored = %v, TagsJSON = %v; want stored untagged", arn, ok, r.TagsJSON)
				}
			}
			if n := calls.count("ListFirewallRules"); n != 0 {
				t.Errorf("ListFirewallRules called %d times; want 0 for a shared group", n)
			}
		})
	}
}

// The ARN of an account in another partition reaches the tag op unchanged.
func TestScanRoute53Resolver_OtherPartitionARNs(t *testing.T) {
	gov := "arn:aws-us-gov:route53resolver:us-gov-west-1:123456789012:resolver-endpoint/rslvr-in-gov"
	calls := newR53RCalls()
	handlers := r53rHandlers(calls)
	handlers["ListResolverEndpoints"] = func(in any) (any, error) {
		calls.record("ListResolverEndpoints", in)
		return &route53resolver.ListResolverEndpointsOutput{ResolverEndpoints: []r53rtypes.ResolverEndpoint{{Arn: &gov, Id: sdkaws.String("rslvr-in-gov")}}}, nil
	}
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(handlers, r53rDenied)
	if _, _, err := scanRoute53Resolver(context.Background(), acct, acct.Regions[0], newTestStore(t), testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Contains(calls.sentTagARNs(), gov) {
		t.Errorf("ListTagsForResource sent %v; want it to include %s", calls.sentTagARNs(), gov)
	}
}

// The tagged listings that are not rule-group or endpoint parents are
// written a page at a time: a listing's first-page row is stored before its
// second page is asked for.
func TestScanRoute53Resolver_LargeListingsWrittenPageByPage(t *testing.T) {
	ops := []string{"ListFirewallDomainLists", "ListFirewallRuleGroupAssociations", "ListOutpostResolvers", "ListResolverQueryLogConfigs", "ListResolverRules"}
	st := newTestStore(t)
	handlers := r53rHandlers(newR53RCalls())
	stored := map[string]bool{}
	for _, op := range ops {
		k := r53rKindOf(t, op)
		next := handlers[op]
		handlers[op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				_, stored[op] = rowsByNativeID(t, st, k.typ)[k.arn("a")]
			}
			return next(in)
		}
	}
	if _, _, err := r53rScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, op := range ops {
		if !stored[op] {
			t.Errorf("%s: first page not stored before the second was read", op)
		}
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanRoute53Resolver_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeRoute53ResolverFirewallDomainList)
	calls := newR53RCalls()
	calls.errs[r53rKindOf(t, "ListFirewallDomainLists").arn("a")] = r53rDenied

	_, _, err := r53rScan(context.Background(), st, r53rHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("ListFirewallRuleGroups"); n != 0 {
		t.Errorf("ListFirewallRuleGroups called %d times after the failed write", n)
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the listings after it; a rule group not listed is no rule parent.
func TestScanRoute53Resolver_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	for _, c := range []struct {
		op   string
		lost int
	}{
		{"ListFirewallDomainLists", 1},
		{"ListFirewallRuleGroups", 2},
		{"ListResolverConfigs", 1},
		{"ListResolverRules", 1},
		{"ListFirewallConfigs", 1},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newR53RCalls()
			handlers := r53rHandlers(calls)
			k := r53rKindOf(t, c.op)
			handlers[c.op] = failOnCall(2, r53rBadInput, handlers[c.op])

			total, _, err := r53rScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidRequestException")
			if total != r53rTotalCount-c.lost {
				t.Errorf("total = %d; want %d", total, r53rTotalCount-c.lost)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("a")]; !ok {
				t.Errorf("%s row from the page before the failure missing", k.typ)
			}
			if n := len(rowsByNativeID(t, st, TypeRoute53ResolverResolverEndpointIPAddress)); n != 2 {
				t.Errorf("IP addresses = %d; want 2 from the last phase", n)
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanRoute53Resolver_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := r53rHandlers(newR53RCalls())
	k := r53rKindOf(t, "ListResolverQueryLogConfigAssociations")
	handlers[k.op] = func(in any) (any, error) {
		page := "a"
		if bacField[*string](in, "NextToken") != nil {
			page = "b"
		}
		return k.page(k, k.id(page), sdkaws.String("same")), nil
	}

	total, _, err := r53rScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != r53rTotalCount {
		t.Errorf("total = %d; want %d", total, r53rTotalCount)
	}
}

// An element without the field its row is keyed by is warned about under its
// listing, named by its name or id, or by the fallback text when it has
// none, and is not stored.
func TestScanRoute53Resolver_ElementWithoutKeyWarns(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		page          any
	}{
		{"named", "ListFirewallDomainLists", "dl-x", &route53resolver.ListFirewallDomainListsOutput{FirewallDomainLists: []r53rtypes.FirewallDomainListMetadata{{Name: sdkaws.String("dl-x")}}}},
		{"id only", "ListResolverConfigs", "rslvr-rc-x", &route53resolver.ListResolverConfigsOutput{ResolverConfigs: []r53rtypes.ResolverConfig{{Id: sdkaws.String("rslvr-rc-x")}}}},
		{"no identifier", "ListFirewallRuleGroupAssociations", "(an element with no name or id)", &route53resolver.ListFirewallRuleGroupAssociationsOutput{FirewallRuleGroupAssociations: []r53rtypes.FirewallRuleGroupAssociation{{}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "route53resolver:"+c.op)
			handlers := r53rHandlers(newR53RCalls())
			handlers[c.op] = func(any) (any, error) { return c.page, nil }

			total, _, err := r53rScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != r53rTotalCount-2 {
				t.Errorf("total = %d; want %d", total, r53rTotalCount-2)
			}
			if len(*warns) != 1 || *all != 1 {
				t.Fatalf("warnings = %v (all ops %d); want 1", *warns, *all)
			}
			if w := (*warns)[0]; !strings.Contains(w.Message, c.key) || w.Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning %q (Scope %q) does not name %q in %s/%s", w.Message, w.Scope, c.key, testAccountID, testRegion)
			}
		})
	}
}

// A denied listing warns once under its op; a denial on a later page keeps
// the pages read before it. Every other listing still runs.
func TestScanRoute53Resolver_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, c := range []struct {
		op         string
		call, lost int
	}{
		{"ListResolverRules", 1, 2},
		{"ListResolverRules", 2, 1},
		{"ListResolverEndpoints", 2, 2},
	} {
		t.Run(fmt.Sprintf("%s/page%d", c.op, c.call), func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "route53resolver:"+c.op)
			handlers := r53rHandlers(newR53RCalls())
			handlers[c.op] = failOnCall(c.call, r53rDenied, handlers[c.op])

			total, _, err := r53rScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != r53rTotalCount-c.lost {
				t.Errorf("total = %d; want %d", total, r53rTotalCount-c.lost)
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("route53resolver:%s warnings = %v (all ops %d); want 1", c.op, *warnings, *all)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanRoute53Resolver_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newR53RCalls()
	handlers := r53rHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListResolverEndpoints"]
	handlers["ListResolverEndpoints"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := r53rScan(ctx, st, handlers)
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
	for _, op := range []string{"ListResolverQueryLogConfigs", "ListFirewallRules", "ListResolverEndpointIpAddresses"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanRoute53Resolver_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "route53resolver.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := r53rHandlers(newR53RCalls())
		handlers["ListResolverRules"] = func(any) (any, error) { return nil, nx }
		total, _, err := r53rScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != r53rTotalCount-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, r53rTotalCount-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newR53RCalls()
		calls.errs[r53rKindOf(t, "ListFirewallDomainLists").arn("b")] = nx
		_, _, err := r53rScan(context.Background(), st, r53rHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanRoute53Resolver(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

// The child phases run after every region-wide phase, and a child phase's
// hard error reaches the caller without costing the other child phase its
// rows.
func TestScanRoute53Resolver_ChildPhaseErrorKeepsOtherRows(t *testing.T) {
	for _, c := range []struct{ op, lostType string }{
		{"ListFirewallRules", TypeRoute53ResolverFirewallRule},
		{"ListResolverEndpointIpAddresses", TypeRoute53ResolverResolverEndpointIPAddress},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := r53rHandlers(newR53RCalls())
			handlers[c.op] = func(any) (any, error) { return nil, r53rBadInput }
			total, _, err := r53rScan(context.Background(), st, handlers)
			assertAPICode(t, err, "InvalidRequestException")
			for _, typ := range []string{TypeRoute53ResolverFirewallRule, TypeRoute53ResolverResolverEndpointIPAddress} {
				want := 2
				if typ == c.lostType {
					want = 0
				}
				if n := len(rowsByNativeID(t, st, typ)); n != want {
					t.Errorf("%s rows = %d; want %d", typ, n, want)
				}
			}
			if total != r53rTotalCount-2 {
				t.Errorf("total = %d; want %d", total, r53rTotalCount-2)
			}
		})
	}
}

// A transient error of a region-wide phase is demoted to one warning under
// its op, so that a child phase's hard error still reaches the dispatcher as
// a hard error.
func TestScanRoute53Resolver_TransientDoesNotMaskHardError(t *testing.T) {
	for _, failed := range []string{"ListFirewallRules", "ListResolverEndpointIpAddresses"} {
		t.Run(failed, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "route53resolver:ListFirewallConfigs")
			handlers := r53rHandlers(newR53RCalls())
			handlers["ListFirewallConfigs"] = func(any) (any, error) { return nil, r53rThrottle }
			handlers[failed] = func(any) (any, error) { return nil, r53rBadInput }
			_, _, err := r53rScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidRequestException")
			if *warns != 1 {
				t.Errorf("warnings = %d; want 1 for the demoted transient", *warns)
			}
		})
	}
}
