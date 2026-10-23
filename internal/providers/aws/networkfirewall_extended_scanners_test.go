package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	nfwtypes "github.com/aws/aws-sdk-go-v2/service/networkfirewall/types"
	"github.com/icearp/disco-cli/store"
)

// stubNFW pages ListProxies / ListContainerAssociations with stubPage and
// answers each Describe by ARN. Describes run in describeFanOut goroutines,
// so the recorded calls are guarded by mu.
type stubNFW struct {
	networkfirewallAPI

	proxyPages    [][]nfwtypes.ProxyMetadata
	proxyListErr  error
	proxyDescErr  map[string]error
	nilProxy      map[string]bool
	assocPages    [][]nfwtypes.ContainerAssociationSummary
	assocListErr  error
	assocDescErr  map[string]error
	tags          map[string][]nfwtypes.Tag // Describe tags by ARN
	mu            sync.Mutex
	describedARNs []string
}

func (s *stubNFW) record(arn string) {
	s.mu.Lock()
	s.describedARNs = append(s.describedARNs, arn)
	s.mu.Unlock()
}

func (s *stubNFW) ListProxies(_ context.Context, in *networkfirewall.ListProxiesInput, _ ...func(*networkfirewall.Options)) (*networkfirewall.ListProxiesOutput, error) {
	if err := nfCheckMaxResults(in.MaxResults); err != nil {
		return nil, err
	}
	items, next, err := stubPage(s.proxyPages, in.NextToken, s.proxyListErr)
	if err != nil {
		return nil, err
	}
	return &networkfirewall.ListProxiesOutput{Proxies: items, NextToken: next}, nil
}

func (s *stubNFW) DescribeProxy(_ context.Context, in *networkfirewall.DescribeProxyInput, _ ...func(*networkfirewall.Options)) (*networkfirewall.DescribeProxyOutput, error) {
	arn := sv(in.ProxyArn)
	s.record(arn)
	if err := s.proxyDescErr[arn]; err != nil {
		return nil, err
	}
	if s.nilProxy[arn] {
		return &networkfirewall.DescribeProxyOutput{}, nil
	}
	p := testNFProxy(arn)
	p.Tags = s.tags[arn]
	return &networkfirewall.DescribeProxyOutput{Proxy: p, UpdateToken: sdkaws.String("tok")}, nil
}

func (s *stubNFW) ListContainerAssociations(_ context.Context, in *networkfirewall.ListContainerAssociationsInput, _ ...func(*networkfirewall.Options)) (*networkfirewall.ListContainerAssociationsOutput, error) {
	if err := nfCheckMaxResults(in.MaxResults); err != nil {
		return nil, err
	}
	items, next, err := stubPage(s.assocPages, in.NextToken, s.assocListErr)
	if err != nil {
		return nil, err
	}
	return &networkfirewall.ListContainerAssociationsOutput{ContainerAssociations: items, NextToken: next}, nil
}

func (s *stubNFW) DescribeContainerAssociation(_ context.Context, in *networkfirewall.DescribeContainerAssociationInput, _ ...func(*networkfirewall.Options)) (*networkfirewall.DescribeContainerAssociationOutput, error) {
	arn := sv(in.ContainerAssociationArn)
	s.record(arn)
	if err := s.assocDescErr[arn]; err != nil {
		return nil, err
	}
	out := testNFContainerAssociation(arn)
	out.Tags = s.tags[arn]
	return out, nil
}

// nfModelMaxResults is the MaxResults cap the Smithy model sets on every
// Network Firewall list op. A literal, not nfListMax, so that a wrong
// production constant fails the tests.
const nfModelMaxResults = 100

// nfCheckMaxResults fails a stubbed list call that does not ask for the
// model's page cap.
func nfCheckMaxResults(got *int32) error {
	if got == nil || *got != nfModelMaxResults {
		return fmt.Errorf("MaxResults = %v; want %d", got, nfModelMaxResults)
	}
	return nil
}

var nfCreateTime = time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

func nfProxyARN(name string) string {
	return "arn:aws:network-firewall:" + testRegion + ":" + testAccountID + ":proxy/" + name
}

func nfAssocARN(name string) string {
	return "arn:aws:network-firewall:" + testRegion + ":" + testAccountID + ":container-association/" + name
}

func nfEKSClusterARN(name string) string {
	return "arn:aws:eks:" + testRegion + ":" + testAccountID + ":cluster/" + name
}

func nfProxyMeta(name string) nfwtypes.ProxyMetadata {
	arn := nfProxyARN(name)
	if name == "" {
		arn = ""
	}
	return nfwtypes.ProxyMetadata{Arn: &arn, Name: &name}
}

func nfAssocSummary(name string) nfwtypes.ContainerAssociationSummary {
	arn := nfAssocARN(name)
	if name == "" {
		arn = ""
	}
	return nfwtypes.ContainerAssociationSummary{Arn: &arn, Name: &name}
}

// testNFProxy is the DescribeProxy body for arn; its name is the ARN's last
// path segment so the stored Name pins which body was used.
func testNFProxy(arn string) *nfwtypes.DescribeProxyResource {
	return &nfwtypes.DescribeProxyResource{
		ProxyArn: &arn, ProxyName: sdkaws.String("proxy-" + arn[len(nfProxyARN("")):]),
		ProxyState: nfwtypes.ProxyStateAttached, NatGatewayId: sdkaws.String("nat-1"),
		CreateTime: &nfCreateTime,
	}
}

func testNFContainerAssociation(arn string) *networkfirewall.DescribeContainerAssociationOutput {
	return &networkfirewall.DescribeContainerAssociationOutput{
		ContainerAssociationArn:  &arn,
		ContainerAssociationName: sdkaws.String("assoc-" + arn[len(nfAssocARN("")):]),
		Status:                   nfwtypes.ContainerAssociationStatusActive,
		Type:                     nfwtypes.ContainerMonitoringTypeEks,
		ContainerMonitoringConfigurations: []nfwtypes.ContainerMonitoringConfiguration{
			{ClusterArn: sdkaws.String(nfEKSClusterARN("c1"))},
		},
	}
}

// nfPhase runs one of the two new phases against the shared stub.
type nfPhase struct {
	name, rtype string
	scan        func(context.Context, networkfirewallAPI, *account, string, *store.Store, string) nfResult
	arn         func(string) string
	// set lists the given pages of names, failing the list with listErr once
	// they run out.
	set func(s *stubNFW, listErr error, pages ...[]string)
	// failDescribe makes the describe of name return err.
	failDescribe func(s *stubNFW, name string, err error)
}

var nfPhases = []nfPhase{
	{
		name: "proxies", rtype: TypeNetworkFirewallProxy, scan: scanNFProxies, arn: nfProxyARN,
		set: func(s *stubNFW, listErr error, pages ...[]string) {
			s.proxyListErr = listErr
			for _, names := range pages {
				var page []nfwtypes.ProxyMetadata
				for _, n := range names {
					page = append(page, nfProxyMeta(n))
				}
				s.proxyPages = append(s.proxyPages, page)
			}
		},
		failDescribe: func(s *stubNFW, name string, err error) {
			if s.proxyDescErr == nil {
				s.proxyDescErr = map[string]error{}
			}
			s.proxyDescErr[nfProxyARN(name)] = err
		},
	},
	{
		name: "container associations", rtype: TypeNetworkFirewallContainerAssociation, scan: scanNFContainerAssociations, arn: nfAssocARN,
		set: func(s *stubNFW, listErr error, pages ...[]string) {
			s.assocListErr = listErr
			for _, names := range pages {
				var page []nfwtypes.ContainerAssociationSummary
				for _, n := range names {
					page = append(page, nfAssocSummary(n))
				}
				s.assocPages = append(s.assocPages, page)
			}
		},
		failDescribe: func(s *stubNFW, name string, err error) {
			if s.assocDescErr == nil {
				s.assocDescErr = map[string]error{}
			}
			s.assocDescErr[nfAssocARN(name)] = err
		},
	},
}

func storedNativeIDs(t *testing.T, st *store.Store, rtype string) []string {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{rtype}})
	if err != nil {
		t.Fatalf("ListResources(%s): %v", rtype, err)
	}
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.NativeID)
	}
	slices.Sort(ids)
	return ids
}

func TestScanNFNewPhases_Scenarios(t *testing.T) {
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: network-firewall:Describe")
	cases := []struct {
		name          string
		setup         func(ph nfPhase, s *stubNFW)
		wantNames     []string
		wantListed    []string // of wantNames, those stored as their list element
		wantWarns     int
		wantErr       string
		wantTransient bool
	}{
		{name: "two pages both described and stored, empty arn warned", setup: func(ph nfPhase, s *stubNFW) {
			ph.set(s, nil, []string{"a", ""}, []string{"b"})
		}, wantNames: []string{"a", "b"}, wantWarns: 1},
		{name: "empty", setup: func(ph nfPhase, s *stubNFW) {}},
		{name: "list access denied warns", setup: func(ph nfPhase, s *stubNFW) {
			ph.set(s, denied)
		}, wantWarns: 1},
		{name: "list denied on page two keeps page one", setup: func(ph nfPhase, s *stubNFW) {
			ph.set(s, denied, []string{"a"})
		}, wantNames: []string{"a"}, wantWarns: 1},
		{name: "list other error on page two keeps page one and propagates", setup: func(ph nfPhase, s *stubNFW) {
			ph.set(s, apiErr("InternalServerError", "boom"), []string{"a"})
		}, wantNames: []string{"a"}, wantErr: "InternalServerError"},
		{name: "describe not found skips that row", setup: func(ph nfPhase, s *stubNFW) {
			ph.set(s, nil, []string{"a", "gone"})
			ph.failDescribe(s, "gone", apiErr("ResourceNotFoundException", "gone"))
		}, wantNames: []string{"a"}},
		{name: "describe denied warns once and keeps the list elements", setup: func(ph nfPhase, s *stubNFW) {
			ph.set(s, nil, []string{"a", "d1", "d2"})
			ph.failDescribe(s, "d1", denied)
			ph.failDescribe(s, "d2", denied)
		}, wantNames: []string{"a", "d1", "d2"}, wantListed: []string{"d1", "d2"}, wantWarns: 1},
		{name: "describe transient keeps the list element and is returned as transient", setup: func(ph nfPhase, s *stubNFW) {
			ph.set(s, nil, []string{"a", "slow"})
			ph.failDescribe(s, "slow", apiErr("ThrottlingException", "rate exceeded"))
		}, wantNames: []string{"a", "slow"}, wantListed: []string{"slow"}, wantTransient: true},
		{name: "describe other error keeps the list element and propagates", setup: func(ph nfPhase, s *stubNFW) {
			ph.set(s, nil, []string{"a", "b"})
			ph.failDescribe(s, "b", apiErr("InvalidRequestException", "bad arn"))
		}, wantNames: []string{"a", "b"}, wantListed: []string{"b"}, wantErr: "InvalidRequestException"},
	}
	for _, ph := range nfPhases {
		for _, tc := range cases {
			t.Run(ph.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				stub := &stubNFW{}
				tc.setup(ph, stub)
				res := ph.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
				switch {
				case tc.wantErr != "" && !isAPIErrorCode(res.err, tc.wantErr):
					t.Fatalf("err = %v; want %s", res.err, tc.wantErr)
				case tc.wantErr == "" && res.err != nil:
					t.Fatalf("err = %v", res.err)
				case tc.wantTransient != isAPIErrorCode(res.transient, "ThrottlingException"):
					t.Fatalf("transient = %v; want it set: %v", res.transient, tc.wantTransient)
				}
				var want []string
				for _, n := range tc.wantNames {
					want = append(want, ph.arn(n))
				}
				if got := storedNativeIDs(t, st, ph.rtype); !slices.Equal(got, want) || res.total != len(want) {
					t.Errorf("stored %v (total %d); want %v", got, res.total, want)
				}
				for _, n := range tc.wantListed {
					if r, _ := nfStoredRow(t, st, ph.arn(n)); sv(r.Name) != n || r.Status != nil {
						t.Errorf("row %s = name %q status %v; want the list element", n, sv(r.Name), r.Status)
					}
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
				}
			})
		}
	}
}

// A preview region gap on the first ListProxies page is silent; after a page
// succeeded the op exists in the region, so a later gap is an error that
// keeps the rows read.
func TestScanNFProxies_PreviewRegionGap(t *testing.T) {
	gap := apiErr("InvalidRequestException", "The API being called does not exist")
	t.Run("first page", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		stub := &stubNFW{}
		nfPhases[0].set(stub, gap)
		res := scanNFProxies(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
		if res.err != nil || res.total != 0 || *warns != 0 {
			t.Errorf("err %v total %d warnings %d; want the gap skipped silently", res.err, res.total, *warns)
		}
	})
	t.Run("page two", func(t *testing.T) {
		st := newTestStore(t)
		stub := &stubNFW{}
		nfPhases[0].set(stub, gap, []string{"a"})
		res := scanNFProxies(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
		if !isAPIErrorCode(res.err, "InvalidRequestException") {
			t.Fatalf("err = %v; want the later-page gap returned", res.err)
		}
		if got := storedNativeIDs(t, st, TypeNetworkFirewallProxy); !slices.Equal(got, []string{nfProxyARN("a")}) {
			t.Errorf("stored %v; want page one's proxy kept", got)
		}
	})
}

// The preview gap is a proxy-feature condition; container associations are a
// separate feature where it has not been observed, so it must surface.
func TestScanNFContainerAssociations_InvalidRequestPropagates(t *testing.T) {
	st := newTestStore(t)
	stub := &stubNFW{assocListErr: apiErr("InvalidRequestException", "The API being called does not exist")}
	if res := scanNFContainerAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); !isAPIErrorCode(res.err, "InvalidRequestException") {
		t.Fatalf("err = %v; want InvalidRequestException", res.err)
	}
}

// A DescribeProxy with no Proxy payload keeps the listed proxy as its list
// element and returns an error, as the other described types do.
func TestScanNFProxies_NilBodyKeepsListElementAndErrs(t *testing.T) {
	st := newTestStore(t)
	stub := &stubNFW{nilProxy: map[string]bool{nfProxyARN("empty"): true}}
	nfPhases[0].set(stub, nil, []string{"a", "empty"})
	res := scanNFProxies(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if res.err == nil {
		t.Fatal("err = nil; want the empty payload reported")
	}
	if got := storedNativeIDs(t, st, TypeNetworkFirewallProxy); !slices.Equal(got, []string{nfProxyARN("a"), nfProxyARN("empty")}) {
		t.Errorf("stored %v; want both proxies", got)
	}
	if r, _ := nfStoredRow(t, st, nfProxyARN("empty")); sv(r.Name) != "empty" {
		t.Errorf("empty proxy name = %q; want the list element's", sv(r.Name))
	}
}

func nfStoredRow(t *testing.T, st *store.Store, nativeID string) (*store.Resource, map[string]any) {
	t.Helper()
	r, err := st.GetResource(store.ResourceID("aws", testAccountID, nativeID))
	if err != nil {
		t.Fatalf("row %s missing: %v", nativeID, err)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s attrs: %v", nativeID, err)
	}
	return r, attrs
}

func TestScanNFProxies_StoredRow(t *testing.T) {
	st := newTestStore(t)
	stub := &stubNFW{tags: map[string][]nfwtypes.Tag{nfProxyARN("a"): nfTags("env", "prod")}}
	nfPhases[0].set(stub, nil, []string{"a", "untagged"})
	if res := scanNFProxies(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); res.err != nil {
		t.Fatal(res.err)
	}
	r, attrs := nfStoredRow(t, st, nfProxyARN("a"))
	if r.Type != TypeNetworkFirewallProxy || sv(r.Region) != testRegion || sv(r.Name) != "proxy-a" || sv(r.Status) != "ATTACHED" {
		t.Errorf("row = type %q region %q name %q status %q", r.Type, sv(r.Region), sv(r.Name), sv(r.Status))
	}
	if sv(r.CreatedAt) != "2026-07-01T12:00:00Z" {
		t.Errorf("CreatedAt = %q", sv(r.CreatedAt))
	}
	if attrs["NatGatewayId"] != "nat-1" {
		t.Errorf("attrs[NatGatewayId] = %v; want nat-1 (the DescribeProxy body)", attrs["NatGatewayId"])
	}
	if _, ok := attrs["UpdateToken"]; ok {
		t.Error("attrs carry the DescribeProxy envelope's UpdateToken; want the Proxy payload only")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"env": "prod"})
	if untagged, _ := nfStoredRow(t, st, nfProxyARN("untagged")); untagged.TagsJSON != nil {
		t.Errorf("untagged proxy tags = %s; want none", *untagged.TagsJSON)
	}
}

func TestScanNFContainerAssociations_StoredRow(t *testing.T) {
	st := newTestStore(t)
	stub := &stubNFW{tags: map[string][]nfwtypes.Tag{nfAssocARN("a"): nfTags("env", "prod")}}
	nfPhases[1].set(stub, nil, []string{"a", "untagged"})
	if res := scanNFContainerAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); res.err != nil {
		t.Fatal(res.err)
	}
	r, attrs := nfStoredRow(t, st, nfAssocARN("a"))
	if r.Type != TypeNetworkFirewallContainerAssociation || sv(r.Region) != testRegion || sv(r.Name) != "assoc-a" || sv(r.Status) != "ACTIVE" {
		t.Errorf("row = type %q region %q name %q status %q", r.Type, sv(r.Region), sv(r.Name), sv(r.Status))
	}
	cfgs, _ := attrs["ContainerMonitoringConfigurations"].([]any)
	if len(cfgs) != 1 || cfgs[0].(map[string]any)["ClusterArn"] != nfEKSClusterARN("c1") {
		t.Errorf("attrs[ContainerMonitoringConfigurations] = %v; want the c1 cluster", attrs["ContainerMonitoringConfigurations"])
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"env": "prod"})
	if untagged, _ := nfStoredRow(t, st, nfAssocARN("untagged")); untagged.TagsJSON != nil {
		t.Errorf("untagged container association tags = %s; want none", *untagged.TagsJSON)
	}
}

const nfProxyRuleGroupARN = "arn:aws:network-firewall:" + testRegion + ":" + testAccountID + ":proxy-rule-group/prg"

// nfTopLevelResponses answers every scanNetworkFirewall op: one rule group
// (a core phase), one proxy rule group (the last pre-existing phase), one
// proxy and one container association; everything else is empty. override
// replaces the queue for the named ops.
func nfTopLevelResponses(override map[string][]stubCall) map[string][]stubCall {
	responses := map[string][]stubCall{
		"ListFirewalls":        {{Output: &networkfirewall.ListFirewallsOutput{}}},
		"ListFirewallPolicies": {{Output: &networkfirewall.ListFirewallPoliciesOutput{}}},
		"ListRuleGroups": {{Output: &networkfirewall.ListRuleGroupsOutput{
			RuleGroups: []nfwtypes.RuleGroupMetadata{{Arn: sdkaws.String(nfRuleGroupARN(testRegion, testAccountID, "rg")), Name: sdkaws.String("rg")}},
		}}},
		"DescribeRuleGroup": {{Output: &networkfirewall.DescribeRuleGroupOutput{
			RuleGroupResponse: &nfwtypes.RuleGroupResponse{RuleGroupArn: sdkaws.String(nfRuleGroupARN(testRegion, testAccountID, "rg")), RuleGroupName: sdkaws.String("rg")},
		}}},
		"ListTLSInspectionConfigurations": {{Output: &networkfirewall.ListTLSInspectionConfigurationsOutput{}}},
		"ListVpcEndpointAssociations":     {{Output: &networkfirewall.ListVpcEndpointAssociationsOutput{}}},
		"ListProxyConfigurations":         {{Output: &networkfirewall.ListProxyConfigurationsOutput{}}},
		"ListProxyRuleGroups": {{Output: &networkfirewall.ListProxyRuleGroupsOutput{
			ProxyRuleGroups: []nfwtypes.ProxyRuleGroupMetadata{{Arn: sdkaws.String(nfProxyRuleGroupARN), Name: sdkaws.String("prg")}},
		}}},
		"ListProxies":   {{Output: &networkfirewall.ListProxiesOutput{Proxies: []nfwtypes.ProxyMetadata{nfProxyMeta("p1")}}}},
		"DescribeProxy": {{Output: &networkfirewall.DescribeProxyOutput{Proxy: testNFProxy(nfProxyARN("p1"))}}},
		"ListContainerAssociations": {{Output: &networkfirewall.ListContainerAssociationsOutput{
			ContainerAssociations: []nfwtypes.ContainerAssociationSummary{nfAssocSummary("c1")},
		}}},
		"DescribeContainerAssociation": {{Output: testNFContainerAssociation(nfAssocARN("c1"))}},
	}
	for op, calls := range override {
		responses[op] = calls
	}
	return responses
}

func TestScanNetworkFirewall_StoresProxyAndContainerAssociationRows(t *testing.T) {
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stubResponses(t, nfTopLevelResponses(nil)), testRegion)}
	total, _, err := scanNetworkFirewall(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanNetworkFirewall: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (rule group, proxy rule group, proxy, container association)", total)
	}
	for rtype, want := range map[string]string{
		TypeNetworkFirewallRuleGroup:            nfRuleGroupARN(testRegion, testAccountID, "rg"),
		TypeNetworkFirewallProxyRuleGroup:       nfProxyRuleGroupARN,
		TypeNetworkFirewallProxy:                nfProxyARN("p1"),
		TypeNetworkFirewallContainerAssociation: nfAssocARN("c1"),
	} {
		if got := storedNativeIDs(t, st, rtype); !slices.Equal(got, []string{want}) {
			t.Errorf("%s rows = %v; want [%s]", rtype, got, want)
		}
	}
}

// TestScanNetworkFirewall_NewPhaseErrorAfterExistingPhases checks that each
// new op's error propagates while the pre-existing phases' rows, up to the
// last of them (proxy rule groups), are kept.
func TestScanNetworkFirewall_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	boom := []stubCall{{Err: apiErr("InternalServerError", "boom")}}
	for _, op := range []string{"ListProxies", "DescribeProxy", "ListContainerAssociations", "DescribeContainerAssociation"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			responses := nfTopLevelResponses(map[string][]stubCall{op: boom})
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stubResponses(t, responses), testRegion)}
			if _, _, err := scanNetworkFirewall(context.Background(), acct, testRegion, st, testScanID); !isAPIErrorCode(err, "InternalServerError") {
				t.Fatalf("err = %v; want the %s InternalServerError", err, op)
			}
			for _, rtype := range []string{TypeNetworkFirewallRuleGroup, TypeNetworkFirewallProxyRuleGroup} {
				if got := storedNativeIDs(t, st, rtype); len(got) != 1 {
					t.Errorf("%s rows = %v; want 1 (existing phases run before %s)", rtype, got, op)
				}
			}
		})
	}
}
