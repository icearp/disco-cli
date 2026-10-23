package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	nfwtypes "github.com/aws/aws-sdk-go-v2/service/networkfirewall/types"
	"github.com/icearp/disco-cli/store"
)

// nfListed is one list element: a resource's ARN and name.
type nfListed struct{ arn, name string }

// nfDescribedCase drives one storeNFDescribed phase through the SDK stub.
type nfDescribedCase struct {
	name, rtype, kind string
	ops               nfOps
	scan              func(context.Context, networkfirewallAPI, *account, string, *store.Store, string) nfResult
	// listARNKey and descARNKey name the ARN field of the list element and
	// of the Describe payload stored under payloadKey.
	listARNKey, payloadKey, descARNKey string
	// listIn fails the test when a content input that decides what the list
	// returns is set, and returns the token and MaxResults sent.
	listIn  func(t *testing.T, in any) (token *string, maxResults *int32)
	listOut func(items []nfListed, next *string) any
	descARN func(in any) string
	// descOut is the Describe output for arn; nil payload when empty.
	descOut func(arn, name string, tags []nfwtypes.Tag, empty bool) any
}

func nfARN(kind, name string) string {
	return "arn:aws:network-firewall:" + testRegion + ":" + testAccountID + ":" + kind + "/" + name
}

var nfDescribedCases = []nfDescribedCase{
	{
		name: "firewalls", rtype: TypeNetworkFirewallFirewall, kind: "firewall", ops: nfOps{"network-firewall:ListFirewalls", "network-firewall:DescribeFirewall"}, scan: scanNetworkFirewalls,
		listARNKey: "FirewallArn", payloadKey: "Firewall", descARNKey: "FirewallArn",
		listIn: func(t *testing.T, in any) (*string, *int32) {
			i := in.(*networkfirewall.ListFirewallsInput)
			if len(i.VpcIds) != 0 {
				t.Errorf("ListFirewalls VpcIds = %v; want unset (every firewall)", i.VpcIds)
			}
			return i.NextToken, i.MaxResults
		},
		listOut: func(items []nfListed, next *string) any {
			out := &networkfirewall.ListFirewallsOutput{NextToken: next}
			for _, it := range items {
				out.Firewalls = append(out.Firewalls, nfwtypes.FirewallMetadata{FirewallArn: sdkaws.String(it.arn), FirewallName: sdkaws.String(it.name)})
			}
			return out
		},
		descARN: func(in any) string { return sv(in.(*networkfirewall.DescribeFirewallInput).FirewallArn) },
		descOut: func(arn, name string, tags []nfwtypes.Tag, empty bool) any {
			if empty {
				return &networkfirewall.DescribeFirewallOutput{UpdateToken: sdkaws.String("tok")}
			}
			return &networkfirewall.DescribeFirewallOutput{Firewall: &nfwtypes.Firewall{
				FirewallArn: &arn, FirewallName: &name, VpcId: sdkaws.String("vpc-1"), Tags: tags,
			}, UpdateToken: sdkaws.String("tok")}
		},
	},
	{
		name: "firewall policies", rtype: TypeNetworkFirewallFirewallPolicy, kind: "firewall-policy", ops: nfOps{"network-firewall:ListFirewallPolicies", "network-firewall:DescribeFirewallPolicy"}, scan: scanNetworkFirewallPolicies,
		listARNKey: "Arn", payloadKey: "FirewallPolicyResponse", descARNKey: "FirewallPolicyArn",
		listIn: func(_ *testing.T, in any) (*string, *int32) {
			i := in.(*networkfirewall.ListFirewallPoliciesInput)
			return i.NextToken, i.MaxResults
		},
		listOut: func(items []nfListed, next *string) any {
			out := &networkfirewall.ListFirewallPoliciesOutput{NextToken: next}
			for _, it := range items {
				out.FirewallPolicies = append(out.FirewallPolicies, nfwtypes.FirewallPolicyMetadata{Arn: sdkaws.String(it.arn), Name: sdkaws.String(it.name)})
			}
			return out
		},
		descARN: func(in any) string {
			return sv(in.(*networkfirewall.DescribeFirewallPolicyInput).FirewallPolicyArn)
		},
		descOut: func(arn, name string, tags []nfwtypes.Tag, empty bool) any {
			if empty {
				return &networkfirewall.DescribeFirewallPolicyOutput{UpdateToken: sdkaws.String("tok")}
			}
			return &networkfirewall.DescribeFirewallPolicyOutput{FirewallPolicyResponse: &nfwtypes.FirewallPolicyResponse{
				FirewallPolicyArn: &arn, FirewallPolicyName: &name, FirewallPolicyId: sdkaws.String("id"), Tags: tags,
			}, UpdateToken: sdkaws.String("tok")}
		},
	},
	{
		name: "rule groups", rtype: TypeNetworkFirewallRuleGroup, kind: "stateful-rulegroup", ops: nfOps{"network-firewall:ListRuleGroups", "network-firewall:DescribeRuleGroup"}, scan: scanNetworkFirewallRuleGroups,
		listARNKey: "Arn", payloadKey: "RuleGroupResponse", descARNKey: "RuleGroupArn",
		listIn: func(t *testing.T, in any) (*string, *int32) {
			i := in.(*networkfirewall.ListRuleGroupsInput)
			if i.Scope != "" || i.Type != "" || i.ManagedType != "" || i.SubscriptionStatus != "" {
				t.Errorf("ListRuleGroups filters = Scope %q Type %q ManagedType %q SubscriptionStatus %q; want all unset (the account's own rule groups)",
					i.Scope, i.Type, i.ManagedType, i.SubscriptionStatus)
			}
			return i.NextToken, i.MaxResults
		},
		listOut: func(items []nfListed, next *string) any {
			out := &networkfirewall.ListRuleGroupsOutput{NextToken: next}
			for _, it := range items {
				out.RuleGroups = append(out.RuleGroups, nfwtypes.RuleGroupMetadata{Arn: sdkaws.String(it.arn), Name: sdkaws.String(it.name)})
			}
			return out
		},
		descARN: func(in any) string { return sv(in.(*networkfirewall.DescribeRuleGroupInput).RuleGroupArn) },
		descOut: func(arn, name string, tags []nfwtypes.Tag, empty bool) any {
			if empty {
				return &networkfirewall.DescribeRuleGroupOutput{UpdateToken: sdkaws.String("tok")}
			}
			return &networkfirewall.DescribeRuleGroupOutput{RuleGroupResponse: &nfwtypes.RuleGroupResponse{
				RuleGroupArn: &arn, RuleGroupName: &name, RuleGroupId: sdkaws.String("id"), Tags: tags,
			}, UpdateToken: sdkaws.String("tok")}
		},
	},
	{
		name: "TLS inspection configurations", rtype: TypeNetworkFirewallTLSInspectionConfiguration, kind: "tls-configuration",
		ops: nfOps{"networkfirewall:ListTLSInspectionConfigurations", "networkfirewall:DescribeTLSInspectionConfiguration"}, scan: scanNFTLSInspectionConfigurations,
		listARNKey: "Arn", payloadKey: "TLSInspectionConfigurationResponse", descARNKey: "TLSInspectionConfigurationArn",
		listIn: func(_ *testing.T, in any) (*string, *int32) {
			i := in.(*networkfirewall.ListTLSInspectionConfigurationsInput)
			return i.NextToken, i.MaxResults
		},
		listOut: func(items []nfListed, next *string) any {
			out := &networkfirewall.ListTLSInspectionConfigurationsOutput{NextToken: next}
			for _, it := range items {
				out.TLSInspectionConfigurations = append(out.TLSInspectionConfigurations,
					nfwtypes.TLSInspectionConfigurationMetadata{Arn: sdkaws.String(it.arn), Name: sdkaws.String(it.name)})
			}
			return out
		},
		descARN: func(in any) string {
			return sv(in.(*networkfirewall.DescribeTLSInspectionConfigurationInput).TLSInspectionConfigurationArn)
		},
		descOut: func(arn, name string, tags []nfwtypes.Tag, empty bool) any {
			if empty {
				return &networkfirewall.DescribeTLSInspectionConfigurationOutput{UpdateToken: sdkaws.String("tok")}
			}
			return &networkfirewall.DescribeTLSInspectionConfigurationOutput{TLSInspectionConfigurationResponse: &nfwtypes.TLSInspectionConfigurationResponse{
				TLSInspectionConfigurationArn: &arn, TLSInspectionConfigurationName: &name,
				TLSInspectionConfigurationId: sdkaws.String("id"), Tags: tags,
			}, UpdateToken: sdkaws.String("tok")}
		},
	},
}

// nfDescribedStub is what the stubbed service holds for one phase. Page i of
// pages is sent for NextToken "t<i>" (none for page 0); after the last page
// the list fails with listErr if set. repeatToken makes the second page
// return the token it was sent. Describe answers by the resource's name: its
// error, an empty payload, or the body named "desc-<name>" with its tags.
type nfDescribedStub struct {
	pages       [][]string
	listErr     error
	repeatToken bool
	descErr     map[string]error
	empty       map[string]bool
	tags        map[string][]nfwtypes.Tag
}

type nfDescribedRun struct {
	st                   *store.Store
	total, inserted      int
	err, transient       error
	listed               []string
	listWarns, descWarns *int
}

// nfListHandler answers op's list calls from s, pinning MaxResults to the
// model cap and NextToken to the token of the page each call should read.
func nfListHandler(t *testing.T, op, kind string, s nfDescribedStub, listIn func(*testing.T, any) (*string, *int32), listOut func([]nfListed, *string) any) func(any) (any, error) {
	listCalls := 0
	return func(in any) (any, error) {
		token, maxResults := listIn(t, in)
		if maxResults == nil || *maxResults != nfModelMaxResults {
			t.Errorf("%s MaxResults = %v; want %d", op, maxResults, nfModelMaxResults)
		}
		if listCalls++; listCalls > len(s.pages)+1 {
			return nil, fmt.Errorf("%s called %d times; the token is not carried", op, listCalls)
		}
		page := 0
		if token != nil {
			if _, err := fmt.Sscanf(*token, "t%d", &page); err != nil {
				return nil, fmt.Errorf("unexpected NextToken %q", *token)
			}
		}
		if want := listCalls - 1; page != want {
			t.Errorf("%s call %d sent the token of page %d; want page %d", op, listCalls, page, want)
		}
		if page >= len(s.pages) {
			return nil, s.listErr
		}
		var next *string
		if page+1 < len(s.pages) || s.listErr != nil {
			next = sdkaws.String(fmt.Sprintf("t%d", page+1))
		}
		if s.repeatToken && page == 1 {
			next = token
		}
		var items []nfListed
		for _, n := range s.pages[page] {
			arn := nfARN(kind, n)
			if n == "" {
				arn = ""
			}
			items = append(items, nfListed{arn: arn, name: n})
		}
		return listOut(items, next), nil
	}
}

// nfOpName is the SDK operation name in an op label such as
// "networkfirewall:ListFirewalls(logging)".
func nfOpName(label string) string {
	_, op, _ := strings.Cut(label, ":")
	op, _, _ = strings.Cut(op, "(")
	return op
}

func (c nfDescribedCase) run(t *testing.T, s nfDescribedStub) nfDescribedRun {
	t.Helper()
	list := nfListHandler(t, c.ops.list, c.kind, s, c.listIn, c.listOut)
	describe := func(in any) (any, error) {
		arn := c.descARN(in)
		name := arn[strings.LastIndex(arn, "/")+1:]
		if err := s.descErr[name]; err != nil {
			return nil, err
		}
		return c.descOut(arn, "desc-"+name, s.tags[name], s.empty[name]), nil
	}
	cfg := opStubConfig(map[string]func(any) (any, error){
		nfOpName(c.ops.list):     list,
		nfOpName(c.ops.describe): describe,
	}, errors.New("unexpected op"))
	r := nfDescribedRun{st: newTestStore(t)}
	r.listWarns = countOpWarnings(r.st, c.ops.list)
	r.descWarns = countOpWarnings(r.st, c.ops.describe)
	client := networkfirewall.NewFromConfig(cfg)
	res := c.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, r.st, testScanID)
	r.total, r.inserted, r.err, r.transient, r.listed = res.total, res.inserted, res.err, res.transient, res.listed
	return r
}

// assertRows checks the stored rows: described names are stored from their
// Describe body with tags (want[name] nil for a body without tags), listed
// names from their list element without tags, and nothing else.
func (c nfDescribedCase) assertRows(t *testing.T, st *store.Store, described map[string]map[string]string, listed ...string) {
	t.Helper()
	rows := rowsByNativeID(t, st, c.rtype)
	if len(rows) != len(described)+len(listed) {
		t.Errorf("stored %d rows; want %d (described %v, listed %v)", len(rows), len(described)+len(listed), described, listed)
	}
	for name, tags := range described {
		arn := nfARN(c.kind, name)
		r, ok := rows[arn]
		if !ok {
			t.Errorf("row %s missing", arn)
			continue
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
		}
		payload, _ := attrs[c.payloadKey].(map[string]any)
		if sv(r.Name) != "desc-"+name || payload[c.descARNKey] != arn {
			t.Errorf("row %s = name %q attrs %s; want the Describe body", arn, sv(r.Name), r.AttributesJSON)
		}
		if tags == nil {
			if r.TagsJSON != nil {
				t.Errorf("row %s tags = %s; want none", arn, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, tags)
	}
	for _, name := range listed {
		arn := nfARN(c.kind, name)
		r, ok := rows[arn]
		if !ok {
			t.Errorf("row %s missing", arn)
			continue
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
		}
		if sv(r.Name) != name || attrs[c.listARNKey] != arn || r.TagsJSON != nil {
			t.Errorf("row %s = name %q attrs %s tags %v; want the list element without tags", arn, sv(r.Name), r.AttributesJSON, r.TagsJSON)
		}
	}
}

func nfTags(kv ...string) []nfwtypes.Tag {
	var tags []nfwtypes.Tag
	for i := 0; i < len(kv); i += 2 {
		tags = append(tags, nfwtypes.Tag{Key: sdkaws.String(kv[i]), Value: sdkaws.String(kv[i+1])})
	}
	return tags
}

func TestStoreNFDescribed_StoresDescribeBodyWithTags(t *testing.T) {
	for _, c := range nfDescribedCases {
		t.Run(c.name, func(t *testing.T) {
			r := c.run(t, nfDescribedStub{
				pages: [][]string{{"a", "untagged"}, {"b"}},
				tags:  map[string][]nfwtypes.Tag{"a": nfTags("env", "prod", "team", "net"), "b": nfTags("env", "dev")},
			})
			if r.err != nil {
				t.Fatalf("err = %v", r.err)
			}
			if r.total != 3 || r.inserted != 3 {
				t.Errorf("total, inserted = %d, %d; want 3, 3", r.total, r.inserted)
			}
			c.assertRows(t, r.st, map[string]map[string]string{
				"a": {"env": "prod", "team": "net"}, "untagged": nil, "b": {"env": "dev"},
			})
			if *r.listWarns+*r.descWarns != 0 {
				t.Errorf("warnings = %d, %d; want none", *r.listWarns, *r.descWarns)
			}
		})
	}
}

func TestStoreNFDescribed_DescribeFailures(t *testing.T) {
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: network-firewall:Describe")
	tags := map[string][]nfwtypes.Tag{"a": nfTags("env", "prod")}
	cases := []struct {
		name                 string
		stub                 nfDescribedStub
		wantListed           []string
		wantErrCode          string // "" with wantErr: an error with no API code
		wantErr              bool
		wantTransient        bool // a ThrottlingException comes back as res.transient
		listWarns, descWarns int
	}{
		{name: "not found after list is not stored", stub: nfDescribedStub{
			descErr: map[string]error{"gone": apiErr("ResourceNotFoundException", "gone")},
		}},
		{name: "denied keeps the list elements and warns once", stub: nfDescribedStub{
			descErr: map[string]error{"d1": denied, "d2": denied},
		}, wantListed: []string{"d1", "d2"}, descWarns: 1},
		{name: "transient keeps the list element and is returned", stub: nfDescribedStub{
			descErr: map[string]error{"slow": apiErr("ThrottlingException", "rate exceeded")},
		}, wantListed: []string{"slow"}, wantTransient: true},
		{name: "other error keeps the list element and is returned", stub: nfDescribedStub{
			descErr: map[string]error{"bad": apiErr("InvalidRequestException", "bad arn")},
		}, wantListed: []string{"bad"}, wantErr: true, wantErrCode: "InvalidRequestException"},
		{name: "empty payload keeps the list element and is returned", stub: nfDescribedStub{
			empty: map[string]bool{"hollow": true},
		}, wantListed: []string{"hollow"}, wantErr: true},
	}
	for _, c := range nfDescribedCases {
		for _, tc := range cases {
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				s := tc.stub
				s.tags = tags
				s.pages = [][]string{{"a"}}
				for name := range s.descErr {
					s.pages[0] = append(s.pages[0], name)
				}
				for name := range s.empty {
					s.pages[0] = append(s.pages[0], name)
				}
				r := c.run(t, s)
				switch {
				case !tc.wantErr && r.err != nil:
					t.Fatalf("err = %v; want nil", r.err)
				case tc.wantErr && r.err == nil:
					t.Fatal("err = nil; want an error")
				case tc.wantErrCode != "" && !isAPIErrorCode(r.err, tc.wantErrCode):
					t.Fatalf("err = %v; want %s", r.err, tc.wantErrCode)
				case tc.wantTransient != isAPIErrorCode(r.transient, "ThrottlingException"):
					t.Fatalf("transient = %v; want it set: %v", r.transient, tc.wantTransient)
				case tc.wantTransient && !isTransientNetworkError(r.transient):
					t.Fatalf("transient = %v; want a transient error", r.transient)
				}
				if r.total != 1+len(tc.wantListed) {
					t.Errorf("total = %d; want %d", r.total, 1+len(tc.wantListed))
				}
				c.assertRows(t, r.st, map[string]map[string]string{"a": {"env": "prod"}}, tc.wantListed...)
				if *r.listWarns != tc.listWarns || *r.descWarns != tc.descWarns {
					t.Errorf("warnings list, describe = %d, %d; want %d, %d", *r.listWarns, *r.descWarns, tc.listWarns, tc.descWarns)
				}
			})
		}
	}
}

func TestStoreNFDescribed_ListFailures(t *testing.T) {
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: network-firewall:List")
	cases := []struct {
		name        string
		stub        nfDescribedStub
		wantRows    []string
		wantErrCode string
		wantRepeat  bool
		listWarns   int
	}{
		{name: "first page denied warns", stub: nfDescribedStub{listErr: denied}, listWarns: 1},
		{name: "first page error is returned", stub: nfDescribedStub{listErr: apiErr("InvalidRequestException", "bad")}, wantErrCode: "InvalidRequestException"},
		{name: "later page denied keeps the rows read and warns", stub: nfDescribedStub{
			pages: [][]string{{"a"}}, listErr: denied,
		}, wantRows: []string{"a"}, listWarns: 1},
		{name: "later page error keeps the rows read and is returned", stub: nfDescribedStub{
			pages: [][]string{{"a"}}, listErr: apiErr("InvalidRequestException", "bad"),
		}, wantRows: []string{"a"}, wantErrCode: "InvalidRequestException"},
		{name: "repeated token keeps the rows read and is returned", stub: nfDescribedStub{
			pages: [][]string{{"a"}, {"b"}}, repeatToken: true,
		}, wantRows: []string{"a", "b"}, wantRepeat: true},
		{name: "empty ARN is not stored and warns", stub: nfDescribedStub{
			pages: [][]string{{"a", ""}},
		}, wantRows: []string{"a"}, listWarns: 1},
	}
	for _, c := range nfDescribedCases {
		for _, tc := range cases {
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				r := c.run(t, tc.stub)
				switch {
				case tc.wantRepeat:
					if !errors.Is(r.err, errRepeatedNextToken) {
						t.Fatalf("err = %v; want errRepeatedNextToken", r.err)
					}
				case tc.wantErrCode != "":
					if !isAPIErrorCode(r.err, tc.wantErrCode) {
						t.Fatalf("err = %v; want %s", r.err, tc.wantErrCode)
					}
				case r.err != nil:
					t.Fatalf("err = %v; want nil", r.err)
				}
				described := map[string]map[string]string{}
				for _, n := range tc.wantRows {
					described[n] = nil
				}
				c.assertRows(t, r.st, described)
				if r.total != len(tc.wantRows) {
					t.Errorf("total = %d; want %d", r.total, len(tc.wantRows))
				}
				if *r.listWarns != tc.listWarns || *r.descWarns != 0 {
					t.Errorf("warnings list, describe = %d, %d; want %d, 0", *r.listWarns, *r.descWarns, tc.listWarns)
				}
			})
		}
	}
}

// A phase's error does not stop the later phases: a failed ListFirewalls
// still leaves the rule group, proxy and container association rows of the
// phases after it.
func TestScanNetworkFirewall_PhaseErrorDoesNotStopLaterPhases(t *testing.T) {
	st := newTestStore(t)
	responses := nfTopLevelResponses(map[string][]stubCall{
		"ListFirewalls": {{Err: apiErr("InvalidRequestException", "bad")}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stubResponses(t, responses), testRegion)}
	total, _, err := scanNetworkFirewall(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InvalidRequestException") {
		t.Fatalf("err = %v; want the ListFirewalls InvalidRequestException", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (rule group, proxy rule group, proxy, container association)", total)
	}
	for _, rtype := range []string{TypeNetworkFirewallRuleGroup, TypeNetworkFirewallProxy, TypeNetworkFirewallContainerAssociation} {
		if got := storedNativeIDs(t, st, rtype); len(got) != 1 {
			t.Errorf("%s rows = %v; want 1 (its phase runs after the failed one)", rtype, got)
		}
	}
}

// A throttled older phase beside a hard error in a newer one is demoted to a
// warning, so the scan reports the hard error, not a transient glitch.
func TestScanNetworkFirewall_TransientBesideHardErrorIsDemoted(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, nfFirewallOps.list)
	responses := nfTopLevelResponses(map[string][]stubCall{
		"ListFirewalls":                {{Err: apiErr("ThrottlingException", "rate exceeded")}},
		"DescribeContainerAssociation": {{Err: apiErr("InvalidRequestException", "bad")}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stubResponses(t, responses), testRegion)}
	_, _, err := scanNetworkFirewall(context.Background(), acct, testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v; want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("ListFirewalls warnings = %d; want 1 (the demoted throttle)", *warns)
	}
}

// A throttled DescribeFirewall keeps the firewall as its list element and
// marks the scan partial (transient) when it is the only failure, and is
// demoted to one warning beside a hard error.
func TestScanNetworkFirewall_TransientDescribeIsReturned(t *testing.T) {
	fw := nfwtypes.FirewallMetadata{FirewallArn: sdkaws.String(nfARN("firewall", "fw")), FirewallName: sdkaws.String("fw")}
	for _, tc := range []struct {
		name      string
		override  map[string][]stubCall
		want      serviceOutcome
		wantWarns int
	}{
		{name: "alone", want: outcomeTransient},
		{name: "beside a hard error", override: map[string][]stubCall{
			"DescribeContainerAssociation": {{Err: apiErr("InvalidRequestException", "bad")}},
		}, want: outcomeError, wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "network-firewall:DescribeFirewall")
			override := map[string][]stubCall{
				"ListFirewalls":                {{Output: &networkfirewall.ListFirewallsOutput{Firewalls: []nfwtypes.FirewallMetadata{fw}}}},
				"DescribeFirewall":             {{Err: apiErr("ThrottlingException", "rate exceeded")}},
				"DescribeLoggingConfiguration": {{Output: &networkfirewall.DescribeLoggingConfigurationOutput{FirewallArn: fw.FirewallArn}}},
			}
			maps.Copy(override, tc.override)
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stubResponses(t, nfTopLevelResponses(override)), testRegion)}
			_, _, err := scanNetworkFirewall(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v; want %v", err, got, tc.want)
			}
			if *warns != tc.wantWarns {
				t.Errorf("DescribeFirewall warnings = %d; want %d", *warns, tc.wantWarns)
			}
			if got := storedNativeIDs(t, st, TypeNetworkFirewallFirewall); len(got) != 1 {
				t.Errorf("firewall rows = %v; want the listed firewall kept", got)
			}
		})
	}
}

// nfListedCase drives a phase that stores its list elements as they are.
type nfListedCase struct {
	name, rtype, kind, op, arnKey string
	scan                          func(context.Context, networkfirewallAPI, *account, string, *store.Store, string) (int, int, error)
	listIn                        func(*testing.T, any) (*string, *int32)
	listOut                       func([]nfListed, *string) any
	previewGap                    bool // the proxy preview region gap ends the listing silently
	noName                        bool // the element has no name; the ARN names the row
}

var nfListedCases = []nfListedCase{
	{
		name: "proxy configurations", rtype: TypeNetworkFirewallProxyConfiguration, kind: "proxy-configuration",
		op: "networkfirewall:ListProxyConfigurations", arnKey: "Arn", scan: scanNFProxyConfigurations, previewGap: true,
		listIn: func(_ *testing.T, in any) (*string, *int32) {
			i := in.(*networkfirewall.ListProxyConfigurationsInput)
			return i.NextToken, i.MaxResults
		},
		listOut: func(items []nfListed, next *string) any {
			out := &networkfirewall.ListProxyConfigurationsOutput{NextToken: next}
			for _, it := range items {
				out.ProxyConfigurations = append(out.ProxyConfigurations, nfwtypes.ProxyConfigurationMetadata{Arn: sdkaws.String(it.arn), Name: sdkaws.String(it.name)})
			}
			return out
		},
	},
	{
		name: "proxy rule groups", rtype: TypeNetworkFirewallProxyRuleGroup, kind: "proxy-rule-group",
		op: "networkfirewall:ListProxyRuleGroups", arnKey: "Arn", scan: scanNFProxyRuleGroups, previewGap: true,
		listIn: func(_ *testing.T, in any) (*string, *int32) {
			i := in.(*networkfirewall.ListProxyRuleGroupsInput)
			return i.NextToken, i.MaxResults
		},
		listOut: func(items []nfListed, next *string) any {
			out := &networkfirewall.ListProxyRuleGroupsOutput{NextToken: next}
			for _, it := range items {
				out.ProxyRuleGroups = append(out.ProxyRuleGroups, nfwtypes.ProxyRuleGroupMetadata{Arn: sdkaws.String(it.arn), Name: sdkaws.String(it.name)})
			}
			return out
		},
	},
	{
		name: "VPC endpoint associations", rtype: TypeNetworkFirewallVpcEndpointAssociation, kind: "vpc-endpoint-association",
		op: "networkfirewall:ListVpcEndpointAssociations", arnKey: "VpcEndpointAssociationArn", scan: scanNFVpcEndpointAssociations, noName: true,
		listIn: func(_ *testing.T, in any) (*string, *int32) {
			i := in.(*networkfirewall.ListVpcEndpointAssociationsInput)
			return i.NextToken, i.MaxResults
		},
		listOut: func(items []nfListed, next *string) any {
			out := &networkfirewall.ListVpcEndpointAssociationsOutput{NextToken: next}
			for _, it := range items {
				out.VpcEndpointAssociations = append(out.VpcEndpointAssociations, nfwtypes.VpcEndpointAssociationMetadata{VpcEndpointAssociationArn: sdkaws.String(it.arn)})
			}
			return out
		},
	},
}

func TestScanNFListedPhases(t *testing.T) {
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: network-firewall:List")
	gap := apiErr("InvalidRequestException", "The API being called does not exist")
	cases := []struct {
		name        string
		stub        nfDescribedStub
		gapOnly     bool
		wantRows    []string
		wantErrCode string
		wantRepeat  bool
		wantWarns   int
	}{
		{name: "two pages stored, empty ARN warned", stub: nfDescribedStub{pages: [][]string{{"a", ""}, {"b"}}}, wantRows: []string{"a", "b"}, wantWarns: 1},
		{name: "first page denied warns", stub: nfDescribedStub{listErr: denied}, wantWarns: 1},
		{name: "later page denied keeps the rows read and warns", stub: nfDescribedStub{pages: [][]string{{"a"}}, listErr: denied}, wantRows: []string{"a"}, wantWarns: 1},
		{name: "later page error keeps the rows read and is returned", stub: nfDescribedStub{
			pages: [][]string{{"a"}}, listErr: apiErr("InternalServerError", "boom"),
		}, wantRows: []string{"a"}, wantErrCode: "InternalServerError"},
		{name: "repeated token keeps the rows read and is returned", stub: nfDescribedStub{
			pages: [][]string{{"a"}, {"b"}}, repeatToken: true,
		}, wantRows: []string{"a", "b"}, wantRepeat: true},
		{name: "preview gap on the first page is silent", stub: nfDescribedStub{listErr: gap}, gapOnly: true},
		{name: "preview gap on a later page keeps the rows read and is returned", stub: nfDescribedStub{
			pages: [][]string{{"a"}}, listErr: gap,
		}, gapOnly: true, wantRows: []string{"a"}, wantErrCode: "InvalidRequestException"},
	}
	for _, c := range nfListedCases {
		for _, tc := range cases {
			if tc.gapOnly && !c.previewGap {
				continue
			}
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, c.op)
				cfg := opStubConfig(map[string]func(any) (any, error){
					nfOpName(c.op): nfListHandler(t, c.op, c.kind, tc.stub, c.listIn, c.listOut),
				}, errors.New("unexpected op"))
				total, inserted, err := c.scan(context.Background(), networkfirewall.NewFromConfig(cfg), newTestAccount(testAccountID), testRegion, st, testScanID)
				switch {
				case tc.wantRepeat:
					if !errors.Is(err, errRepeatedNextToken) {
						t.Fatalf("err = %v; want errRepeatedNextToken", err)
					}
				case tc.wantErrCode != "":
					if !isAPIErrorCode(err, tc.wantErrCode) {
						t.Fatalf("err = %v; want %s", err, tc.wantErrCode)
					}
				case err != nil:
					t.Fatalf("err = %v; want nil", err)
				}
				if total != len(tc.wantRows) || inserted != len(tc.wantRows) {
					t.Errorf("total, inserted = %d, %d; want %d", total, inserted, len(tc.wantRows))
				}
				rows := rowsByNativeID(t, st, c.rtype)
				if len(rows) != len(tc.wantRows) {
					t.Errorf("stored %d rows; want %v", len(rows), tc.wantRows)
				}
				for _, n := range tc.wantRows {
					arn := nfARN(c.kind, n)
					r, ok := rows[arn]
					if !ok {
						t.Errorf("row %s missing", arn)
						continue
					}
					var attrs map[string]any
					if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
						t.Fatalf("%s attrs: %v", arn, err)
					}
					wantName := n
					if c.noName {
						wantName = arn
					}
					if sv(r.Name) != wantName || attrs[c.arnKey] != arn {
						t.Errorf("row %s = name %q attrs %s; want name %q and the list element", arn, sv(r.Name), r.AttributesJSON, wantName)
					}
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
				}
			})
		}
	}
}

// TestScanNFLoggingConfigurations covers the per-firewall
// DescribeLoggingConfiguration outcomes over the firewalls phase 1 listed.
func TestScanNFLoggingConfigurations(t *testing.T) {
	const describeOp = "networkfirewall:DescribeLoggingConfiguration"
	denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: network-firewall:DescribeLoggingConfiguration")
	cases := []struct {
		name        string
		firewalls   []string
		descErr     map[string]error
		wantRows    []string
		wantErrCode string
		descWarns   int
	}{
		{
			name: "configured firewalls stored, unconfigured and vanished skipped", firewalls: []string{"a", "nolog", "gone", "b"},
			descErr: map[string]error{"gone": apiErr("ResourceNotFoundException", "gone")}, wantRows: []string{"a", "b"},
		},
		{
			name: "denied firewalls warn once, siblings kept", firewalls: []string{"a", "d1", "d2"},
			descErr: map[string]error{"d1": denied, "d2": denied}, wantRows: []string{"a"}, descWarns: 1,
		},
		{
			name: "other describe error is returned, siblings kept", firewalls: []string{"a", "bad", "b"},
			descErr: map[string]error{"bad": apiErr("InvalidRequestException", "bad")}, wantRows: []string{"a", "b"}, wantErrCode: "InvalidRequestException",
		},
		{name: "no firewalls listed", firewalls: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, describeOp)
			describe := func(in any) (any, error) {
				arn := sv(in.(*networkfirewall.DescribeLoggingConfigurationInput).FirewallArn)
				name := arn[strings.LastIndex(arn, "/")+1:]
				if err := tc.descErr[name]; err != nil {
					return nil, err
				}
				lc := &nfwtypes.LoggingConfiguration{}
				if name != "nolog" {
					lc.LogDestinationConfigs = []nfwtypes.LogDestinationConfig{{LogType: nfwtypes.LogTypeFlow, LogDestinationType: nfwtypes.LogDestinationTypeS3}}
				}
				return &networkfirewall.DescribeLoggingConfigurationOutput{FirewallArn: &arn, LoggingConfiguration: lc}, nil
			}
			cfg := opStubConfig(map[string]func(any) (any, error){"DescribeLoggingConfiguration": describe}, errors.New("unexpected op"))
			var arns []string
			for _, n := range tc.firewalls {
				arns = append(arns, nfARN("firewall", n))
			}
			total, _, err := scanNFLoggingConfigurations(context.Background(), networkfirewall.NewFromConfig(cfg), newTestAccount(testAccountID), testRegion, st, testScanID, arns)
			switch {
			case tc.wantErrCode != "" && !isAPIErrorCode(err, tc.wantErrCode):
				t.Fatalf("err = %v; want %s", err, tc.wantErrCode)
			case tc.wantErrCode == "" && err != nil:
				t.Fatalf("err = %v; want nil", err)
			}
			var want []string
			for _, n := range tc.wantRows {
				want = append(want, nfARN("firewall", n)+"/logging-configuration")
			}
			slices.Sort(want)
			if got := storedNativeIDs(t, st, TypeNetworkFirewallLoggingConfiguration); !slices.Equal(got, want) || total != len(want) {
				t.Errorf("stored %v (total %d); want %v", got, total, want)
			}
			if *warns != tc.descWarns {
				t.Errorf("%s warnings = %d; want %d", describeOp, *warns, tc.descWarns)
			}
		})
	}
}

// The logging phase describes the firewalls the firewalls phase read, without
// listing them again (stubResponses fails a second ListFirewalls call): a
// failed second page leaves it the first page's firewall.
func TestScanNetworkFirewall_LoggingUsesFirewallsPhaseARNs(t *testing.T) {
	st := newTestStore(t)
	fwARN := nfARN("firewall", "a")
	responses := nfTopLevelResponses(map[string][]stubCall{
		"ListFirewalls": {
			{Output: &networkfirewall.ListFirewallsOutput{
				Firewalls: []nfwtypes.FirewallMetadata{{FirewallArn: &fwARN, FirewallName: sdkaws.String("a")}}, NextToken: sdkaws.String("t1"),
			}},
			{Err: apiErr("InvalidRequestException", "bad")},
		},
		"DescribeFirewall": {{Output: &networkfirewall.DescribeFirewallOutput{Firewall: &nfwtypes.Firewall{FirewallArn: &fwARN, FirewallName: sdkaws.String("a")}}}},
		"DescribeLoggingConfiguration": {{Output: &networkfirewall.DescribeLoggingConfigurationOutput{
			FirewallArn: &fwARN,
			LoggingConfiguration: &nfwtypes.LoggingConfiguration{LogDestinationConfigs: []nfwtypes.LogDestinationConfig{
				{LogType: nfwtypes.LogTypeAlert, LogDestinationType: nfwtypes.LogDestinationTypeS3},
			}},
		}}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stubResponses(t, responses), testRegion)}
	_, _, err := scanNetworkFirewall(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InvalidRequestException") {
		t.Fatalf("err = %v; want the ListFirewalls page-two error", err)
	}
	if got := storedNativeIDs(t, st, TypeNetworkFirewallLoggingConfiguration); !slices.Equal(got, []string{fwARN + "/logging-configuration"}) {
		t.Errorf("logging rows = %v; want firewall a's", got)
	}
}
