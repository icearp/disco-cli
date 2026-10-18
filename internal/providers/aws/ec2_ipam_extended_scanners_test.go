package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

const (
	ipxCidr1    = "203.0.113.0/24"
	ipxCidr2    = "198.51.100.0/24"
	ipxIpamID   = "ipam-0aaa"
	ipxIpamARN  = "arn:aws:ec2::123456789012:ipam/ipam-0aaa"
	ipxAsn1     = "64512"
	ipxAsn2     = "64513"
	ipxPolicy1  = "ipam-policy-0001"
	ipxPolicy2  = "ipam-policy-0002"
	ipxPolicy3  = "ipam-policy-0003"
	ipxPoolID   = "ipam-pool-0abc"
	ipxLocale   = "us-east-1"
	ipxLocale2  = "None"
	ipxPageNext = "p2"
)

var (
	ipxDenied  = apiErr("UnauthorizedOperation", "You are not authorized to perform this operation. User: arn:aws:iam::123456789012:user/x is not authorized to perform: ec2:Describe")
	errIpxTest = errors.New("ipam extended test failure")
)

func ipxPolicyARN(id string) string { return "arn:aws:ec2::123456789012:ipam-policy/" + id }

func ipxByoipID(cidr string) string { return ec2ARN(testRegion, testAccountID, "byoip-cidr", cidr) }

func ipxByoasnID(asn string) string {
	return ec2ARN(testRegion, testAccountID, "ipam-byoasn", ipxIpamID+"/"+asn)
}

func ipxRuleID(policyID, resourceType, locale string) string {
	return ipamPolicyAllocationRuleNativeID(ipxPolicyARN(policyID), resourceType, locale)
}

func ipxClient(handlers map[string]func(in any) (any, error)) ec2API {
	return ec2.NewFromConfig(opStubConfig(handlers, ipxDenied))
}

func ipxAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	return m
}

func ipxParents(ids ...string) []childParent {
	out := make([]childParent, len(ids))
	for i, id := range ids {
		out[i] = childParent{id: id, arn: ipxPolicyARN(id)}
	}
	return out
}

// ipxSeedPolicies stores the policy rows the allocation-rule pairs link under.
func ipxSeedPolicies(t *testing.T, st *store.Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		upsertTestResource(t, st, "aws", testAccountID, TypeEC2IpamPolicy, ipxPolicyARN(id), testRegion, "{}")
	}
}

func ipxByoipHandler(in any) (any, error) {
	p := in.(*ec2.DescribeByoipCidrsInput)
	if sv32(p.MaxResults) != ec2ByoipCidrsPageLimit {
		return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
	}
	if p.NextToken == nil {
		return &ec2.DescribeByoipCidrsOutput{NextToken: ptrStr(ipxPageNext), ByoipCidrs: []ec2types.ByoipCidr{{
			Cidr: ptrStr(ipxCidr1), State: ec2types.ByoipCidrStateAdvertised, Description: ptrStr("prod range"),
			AsnAssociations: []ec2types.AsnAssociation{{Asn: ptrStr(ipxAsn1), Cidr: ptrStr(ipxCidr1)}},
		}}}, nil
	}
	return &ec2.DescribeByoipCidrsOutput{ByoipCidrs: []ec2types.ByoipCidr{
		{Cidr: ptrStr(ipxCidr2), State: ec2types.ByoipCidrStateProvisioned},
		{Cidr: ptrStr("192.0.2.0/24"), State: ec2types.ByoipCidrStateDeprovisioned},
		{State: ec2types.ByoipCidrStateProvisioned},
	}}, nil
}

func ipxByoasnHandler(in any) (any, error) {
	p := in.(*ec2.DescribeIpamByoasnInput)
	if sv32(p.MaxResults) != ec2IpamByoasnPageLimit {
		return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
	}
	if p.NextToken == nil {
		return &ec2.DescribeIpamByoasnOutput{NextToken: ptrStr(ipxPageNext), Byoasns: []ec2types.Byoasn{{
			Asn: ptrStr(ipxAsn1), IpamId: ptrStr(ipxIpamID), State: ec2types.AsnStateProvisioned, StatusMessage: ptrStr("ok"),
		}}}, nil
	}
	return &ec2.DescribeIpamByoasnOutput{Byoasns: []ec2types.Byoasn{
		{Asn: ptrStr(ipxAsn2), IpamId: ptrStr(ipxIpamID), State: ec2types.AsnStatePendingProvision},
		{Asn: ptrStr("64514"), IpamId: ptrStr(ipxIpamID), State: ec2types.AsnStateDeprovisioned},
		{Asn: ptrStr("64515"), State: ec2types.AsnStateProvisioned},
		{IpamId: ptrStr(ipxIpamID), State: ec2types.AsnStateProvisioned},
	}}, nil
}

// ipxAllocationHandler serves policy 1 over two pages and policy 2 in one,
// recording every policy queried.
func ipxAllocationHandler(calls *parentCalls) func(in any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*ec2.GetIpamPolicyAllocationRulesInput)
		calls.add(sv(p.IpamPolicyId))
		if sv32(p.MaxResults) != ec2IpamPolicyAllocationPageLimit {
			return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
		}
		switch {
		case sv(p.IpamPolicyId) == ipxPolicy2:
			return &ec2.GetIpamPolicyAllocationRulesOutput{IpamPolicyDocuments: []ec2types.IpamPolicyDocument{{
				IpamPolicyId: ptrStr(ipxPolicy2), ResourceType: ec2types.IpamPolicyResourceTypeAlb, Locale: ptrStr(ipxLocale2),
			}}}, nil
		case p.NextToken == nil:
			return &ec2.GetIpamPolicyAllocationRulesOutput{NextToken: ptrStr(ipxPageNext), IpamPolicyDocuments: []ec2types.IpamPolicyDocument{{
				IpamPolicyId: ptrStr(ipxPolicy1), ResourceType: ec2types.IpamPolicyResourceTypeEip, Locale: ptrStr(ipxLocale),
				AllocationRules: []ec2types.IpamPolicyAllocationRule{{SourceIpamPoolId: ptrStr(ipxPoolID)}},
			}}}, nil
		default:
			return &ec2.GetIpamPolicyAllocationRulesOutput{IpamPolicyDocuments: []ec2types.IpamPolicyDocument{
				{IpamPolicyId: ptrStr(ipxPolicy1), ResourceType: ec2types.IpamPolicyResourceTypeRds, Locale: ptrStr(ipxLocale)},
				{IpamPolicyId: ptrStr(ipxPolicy1), Locale: ptrStr(ipxLocale)},
				{IpamPolicyId: ptrStr(ipxPolicy1), ResourceType: ec2types.IpamPolicyResourceTypeRnat},
			}}, nil
		}
	}
}

func sv32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func TestScanEC2ByoipCidrs_StoresLiveRangesAcrossPages(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanEC2ByoipCidrs(context.Background(), ipxClient(map[string]func(any) (any, error){
		"DescribeByoipCidrs": ipxByoipHandler,
	}), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2ByoipCidrs: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	rows := rowsByNativeID(t, st, TypeEC2ByoipCidr)
	if got, want := nativeIDs(rows), []string{ipxByoipID(ipxCidr2), ipxByoipID(ipxCidr1)}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v (deprovisioned and CIDR-less ranges dropped)", got, want)
	}
	r := rows[ipxByoipID(ipxCidr1)]
	if sv(r.Name) != ipxCidr1 || sv(r.Status) != "advertised" || sv(r.Region) != testRegion {
		t.Errorf("name/status/region = %q/%q/%q, want %s/advertised/%s", sv(r.Name), sv(r.Status), sv(r.Region), ipxCidr1, testRegion)
	}
	if got := ipxAttrs(t, r)["Description"]; got != "prod range" {
		t.Errorf("attrs Description = %v, want prod range", got)
	}
	if got := sv(rows[ipxByoipID(ipxCidr2)].Status); got != "provisioned" {
		t.Errorf("range 2 status = %q, want provisioned", got)
	}
}

func TestEC2ByoipNativeIDShape(t *testing.T) {
	if got, want := ipxByoipID(ipxCidr1), "arn:aws:ec2:us-east-1:123456789012:byoip-cidr/203.0.113.0/24"; got != want {
		t.Errorf("byoip NativeID = %q, want %q", got, want)
	}
	if got, want := ipxByoasnID(ipxAsn1), "arn:aws:ec2:us-east-1:123456789012:ipam-byoasn/ipam-0aaa/64512"; got != want {
		t.Errorf("byoasn NativeID = %q, want %q", got, want)
	}
	if got, want := ipxRuleID(ipxPolicy1, "eip", ipxLocale), "arn:aws:ec2::123456789012:ipam-policy/ipam-policy-0001/allocation-rule/eip/us-east-1"; got != want {
		t.Errorf("allocation rule NativeID = %q, want %q", got, want)
	}
}

func TestScanEC2IpamByoasns_StoresLiveAsnsAcrossPages(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanEC2IpamByoasns(context.Background(), ipxClient(map[string]func(any) (any, error){
		"DescribeIpamByoasn": ipxByoasnHandler,
	}), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2IpamByoasns: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	rows := rowsByNativeID(t, st, TypeEC2IpamByoasn)
	if got, want := nativeIDs(rows), []string{ipxByoasnID(ipxAsn1), ipxByoasnID(ipxAsn2)}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v (deprovisioned, IPAM-less and ASN-less entries dropped)", got, want)
	}
	r := rows[ipxByoasnID(ipxAsn1)]
	if sv(r.Name) != ipxAsn1 || sv(r.Status) != "provisioned" || sv(r.Region) != testRegion {
		t.Errorf("name/status/region = %q/%q/%q, want %s/provisioned/%s", sv(r.Name), sv(r.Status), sv(r.Region), ipxAsn1, testRegion)
	}
	if got := ipxAttrs(t, r)["IpamId"]; got != ipxIpamID {
		t.Errorf("attrs IpamId = %v, want %s", got, ipxIpamID)
	}
	if got := sv(rows[ipxByoasnID(ipxAsn2)].Status); got != "pending-provision" {
		t.Errorf("ASN 2 status = %q, want pending-provision", got)
	}
}

// A repeated NextToken ends the loop instead of spinning.
func TestScanEC2IpamByoasns_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	calls := 0
	_, _, err := scanEC2IpamByoasns(context.Background(), ipxClient(map[string]func(any) (any, error){
		"DescribeIpamByoasn": func(any) (any, error) {
			calls++
			if calls > 3 {
				return nil, errIpxTest
			}
			return &ec2.DescribeIpamByoasnOutput{NextToken: ptrStr(ipxPageNext), Byoasns: []ec2types.Byoasn{{
				Asn: ptrStr(ipxAsn1), IpamId: ptrStr(ipxIpamID), State: ec2types.AsnStateProvisioned,
			}}}, nil
		},
	}), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2IpamByoasns: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (stop when the token repeats)", calls)
	}
}

// The region-wide BYOIP and BYOASN phases: an empty result stores nothing, a
// denial warns once, a first-page feature gap skips silently, anything else is
// returned, and an error after a good page keeps that page's rows. They differ
// on a feature gap after a good page: BYOASN returns it as an error, while
// BYOIP inherits ec2PageScan's silent skip.
func TestScanEC2RegionWideIpamPhases_Errors(t *testing.T) {
	phases := []struct {
		name, op, sdkOp string
		typ             string
		run             func(ec2API, *store.Store) (int, int, error)
		empty           any
		firstPage       func(any) (any, error)
		wantFirstPageID string
		// lateGapIsError says whether a feature gap after a good page is returned.
		lateGapIsError bool
	}{
		{
			name: "byoip", op: ec2ByoipCidrsOp, sdkOp: "DescribeByoipCidrs", typ: TypeEC2ByoipCidr,
			run: func(c ec2API, st *store.Store) (int, int, error) {
				return scanEC2ByoipCidrs(context.Background(), c, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
			empty: &ec2.DescribeByoipCidrsOutput{}, firstPage: ipxByoipHandler, wantFirstPageID: ipxByoipID(ipxCidr1),
		},
		{
			name: "byoasn", op: ec2IpamByoasnOp, sdkOp: "DescribeIpamByoasn", typ: TypeEC2IpamByoasn,
			run: func(c ec2API, st *store.Store) (int, int, error) {
				return scanEC2IpamByoasns(context.Background(), c, newTestAccount(testAccountID), testRegion, st, testScanID)
			},
			empty: &ec2.DescribeIpamByoasnOutput{}, firstPage: ipxByoasnHandler, wantFirstPageID: ipxByoasnID(ipxAsn1),
			lateGapIsError: true,
		},
	}
	for _, ph := range phases {
		t.Run(ph.name+"/empty", func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := ph.run(ipxClient(map[string]func(any) (any, error){ph.sdkOp: func(any) (any, error) { return ph.empty, nil }}), st)
			if err != nil || total != 0 {
				t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
			}
			if n := len(rowsByNativeID(t, st, ph.typ)); n != 0 {
				t.Errorf("rows = %d, want 0", n)
			}
		})
		t.Run(ph.name+"/denied", func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, ph.op)
			if _, _, err := ph.run(ipxClient(nil), st); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", ph.op, *warnings)
			}
		})
		t.Run(ph.name+"/feature gap", func(t *testing.T) {
			st := newTestStore(t)
			var warned int
			st.OnWarn = func(store.ScanWarning) { warned++ }
			_, _, err := ph.run(ipxClient(map[string]func(any) (any, error){
				ph.sdkOp: func(any) (any, error) {
					return nil, apiErr("InvalidAction", "The action is not valid for this web service.")
				},
			}), st)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if warned != 0 {
				t.Errorf("warnings = %d, want 0", warned)
			}
		})
		t.Run(ph.name+"/other error", func(t *testing.T) {
			st := newTestStore(t)
			_, _, err := ph.run(ipxClient(map[string]func(any) (any, error){
				ph.sdkOp: func(any) (any, error) { return nil, apiErr("InvalidParameterValue", "bad") },
			}), st)
			if !isAPIErrorCode(err, "InvalidParameterValue") {
				t.Fatalf("err = %v, want the InvalidParameterValue error", err)
			}
		})
		t.Run(ph.name+"/error after a page", func(t *testing.T) {
			st := newTestStore(t)
			_, _, err := ph.run(ipxClient(map[string]func(any) (any, error){
				ph.sdkOp: func(in any) (any, error) {
					if !isFirstPage(in) {
						return nil, errIpxTest
					}
					return ph.firstPage(in)
				},
			}), st)
			if !errors.Is(err, errIpxTest) {
				t.Fatalf("err = %v, want the second-page error", err)
			}
			if _, ok := rowsByNativeID(t, st, ph.typ)[ph.wantFirstPageID]; !ok {
				t.Errorf("first-page row %s missing after a second-page error", ph.wantFirstPageID)
			}
		})
		t.Run(ph.name+"/feature gap after a page", func(t *testing.T) {
			st := newTestStore(t)
			_, _, err := ph.run(ipxClient(map[string]func(any) (any, error){
				ph.sdkOp: func(in any) (any, error) {
					if !isFirstPage(in) {
						return nil, apiErr("InvalidAction", "The action is not valid for this web service.")
					}
					return ph.firstPage(in)
				},
			}), st)
			if ph.lateGapIsError && !isAPIErrorCode(err, "InvalidAction") {
				t.Errorf("err = %v, want the InvalidAction gap", err)
			}
			if !ph.lateGapIsError && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
			if _, ok := rowsByNativeID(t, st, ph.typ)[ph.wantFirstPageID]; !ok {
				t.Errorf("first-page row %s missing after a second-page feature gap", ph.wantFirstPageID)
			}
		})
	}
}

func isFirstPage(in any) bool {
	switch p := in.(type) {
	case *ec2.DescribeByoipCidrsInput:
		return p.NextToken == nil
	case *ec2.DescribeIpamByoasnInput:
		return p.NextToken == nil
	}
	return false
}

func TestScanEC2IpamPolicyAllocationRules_StoresDocumentsUnderPolicies(t *testing.T) {
	st := newTestStore(t)
	ipxSeedPolicies(t, st, ipxPolicy1, ipxPolicy2)
	var calls parentCalls
	total, _, err := scanEC2IpamPolicyAllocationRules(context.Background(), ipxClient(map[string]func(any) (any, error){
		"GetIpamPolicyAllocationRules": ipxAllocationHandler(&calls),
	}), newTestAccount(testAccountID), testRegion, st, testScanID, ipxParents(ipxPolicy1, ipxPolicy2))
	if err != nil {
		t.Fatalf("scanEC2IpamPolicyAllocationRules: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{ipxPolicy1, ipxPolicy2}) {
		t.Errorf("policies queried = %v, want [%s %s]", got, ipxPolicy1, ipxPolicy2)
	}
	rows := rowsByNativeID(t, st, TypeEC2IpamPolicyAllocationRule)
	want := []string{ipxRuleID(ipxPolicy1, "eip", ipxLocale), ipxRuleID(ipxPolicy1, "rds", ipxLocale), ipxRuleID(ipxPolicy2, "alb", ipxLocale2)}
	got := nativeIDs(rows)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v (documents without a resource type or locale dropped)", got, want)
	}
	eip := rows[ipxRuleID(ipxPolicy1, "eip", ipxLocale)]
	if sv(eip.Region) != testRegion {
		t.Errorf("region = %q, want %s", sv(eip.Region), testRegion)
	}
	rules, _ := ipxAttrs(t, eip)["AllocationRules"].([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["SourceIpamPoolId"] != ipxPoolID {
		t.Errorf("attrs AllocationRules = %v, want one rule from %s", rules, ipxPoolID)
	}
	for _, id := range want {
		parent := ipxPolicyARN(ipxPolicy1)
		if id == ipxRuleID(ipxPolicy2, "alb", ipxLocale2) {
			parent = ipxPolicyARN(ipxPolicy2)
		}
		assertContainsNative(t, st, parent, id)
	}
}

func TestScanEC2IpamPolicyAllocationRules_NoPolicies(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanEC2IpamPolicyAllocationRules(context.Background(), ipxClient(nil),
		newTestAccount(testAccountID), testRegion, st, testScanID, nil)
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
}

// Per-policy errors: a policy gone since it was listed and a transient
// failure are skipped with one warning each, keeping the other policies'
// rows; a denial warns once for every policy; anything else fails the phase.
func TestScanEC2IpamPolicyAllocationRules_PerPolicyErrors(t *testing.T) {
	cases := []struct {
		name     string
		failErr  error
		wantErr  error
		wantWarn int
		wantRows bool
	}{
		{"policy gone", apiErr(ec2IpamPolicyNotFound, "The ipam policy ID does not exist"), nil, 1, true},
		{"transient", io.EOF, nil, 1, true},
		{"denied", ipxDenied, nil, 1, true},
		{"other", errIpxTest, errIpxTest, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			ipxSeedPolicies(t, st, ipxPolicy1, ipxPolicy2, ipxPolicy3)
			var calls parentCalls
			inner := ipxAllocationHandler(&calls)
			var warned int
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == ec2IpamPolicyAllocationRulesOp {
					warned++
				}
			}
			_, _, err := scanEC2IpamPolicyAllocationRules(context.Background(), ipxClient(map[string]func(any) (any, error){
				"GetIpamPolicyAllocationRules": func(in any) (any, error) {
					if id := sv(in.(*ec2.GetIpamPolicyAllocationRulesInput).IpamPolicyId); id == ipxPolicy1 || id == ipxPolicy3 {
						return nil, c.failErr
					}
					return inner(in)
				},
			}), newTestAccount(testAccountID), testRegion, st, testScanID, ipxParents(ipxPolicy1, ipxPolicy2, ipxPolicy3))
			if c.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if warned != c.wantWarn {
				t.Errorf("warnings = %d, want %d", warned, c.wantWarn)
			}
			_, ok := rowsByNativeID(t, st, TypeEC2IpamPolicyAllocationRule)[ipxRuleID(ipxPolicy2, "alb", ipxLocale2)]
			if ok != c.wantRows {
				t.Errorf("policy 2 row stored = %v, want %v", ok, c.wantRows)
			}
		})
	}
}

func TestScanIpamPolicies_ReturnsStoredLivePolicies(t *testing.T) {
	st := newTestStore(t)
	client := ipxClient(map[string]func(any) (any, error){
		"DescribeIpamPolicies": func(any) (any, error) {
			return &ec2.DescribeIpamPoliciesOutput{IpamPolicies: []ec2types.IpamPolicy{
				{IpamPolicyId: ptrStr(ipxPolicy1), IpamPolicyArn: ptrStr(ipxPolicyARN(ipxPolicy1)), State: ec2types.IpamPolicyStateCreateComplete},
				{IpamPolicyId: ptrStr(ipxPolicy2), IpamPolicyArn: ptrStr(ipxPolicyARN(ipxPolicy2)), State: ec2types.IpamPolicyStateDeleteComplete},
				{IpamPolicyId: ptrStr(ipxPolicy3), IpamPolicyArn: ptrStr(ipxPolicyARN(ipxPolicy3)), State: ec2types.IpamPolicyStateDeleteInProgress},
				{IpamPolicyArn: ptrStr(ipxPolicyARN("ipam-policy-noid")), State: ec2types.IpamPolicyStateCreateComplete},
			}}, nil
		},
	})
	_, _, policies, err := scanIpamPolicies(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanIpamPolicies: %v", err)
	}
	want := ipxParents(ipxPolicy1, ipxPolicy3)
	if !slices.Equal(policies, want) {
		t.Errorf("policies = %v, want %v (delete-complete and id-less dropped)", policies, want)
	}
	if n := len(rowsByNativeID(t, st, TypeEC2IpamPolicy)); n != 4 {
		t.Errorf("policy rows = %d, want 4", n)
	}
}

// ipxTopLevelHandlers answers the IPAM ops the extended phases and their
// parent need, plus DescribeIpams for an older-phase row; scanEC2 runs every
// other EC2 op against the denial.
func ipxTopLevelHandlers(calls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"DescribeIpams": func(any) (any, error) {
			return &ec2.DescribeIpamsOutput{Ipams: []ec2types.Ipam{{IpamId: ptrStr(ipxIpamID), IpamArn: ptrStr(ipxIpamARN), IpamRegion: ptrStr(testRegion)}}}, nil
		},
		"DescribeIpamPolicies": func(any) (any, error) {
			return &ec2.DescribeIpamPoliciesOutput{IpamPolicies: []ec2types.IpamPolicy{
				{IpamPolicyId: ptrStr(ipxPolicy1), IpamPolicyArn: ptrStr(ipxPolicyARN(ipxPolicy1)), State: ec2types.IpamPolicyStateCreateComplete},
				{IpamPolicyId: ptrStr(ipxPolicy3), IpamPolicyArn: ptrStr(ipxPolicyARN(ipxPolicy3)), State: ec2types.IpamPolicyStateDeleteComplete},
			}}, nil
		},
		"GetIpamPolicyAllocationRules": ipxAllocationHandler(calls),
		"DescribeByoipCidrs":           ipxByoipHandler,
		"DescribeIpamByoasn":           ipxByoasnHandler,
	}
}

func scanEC2TopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ipxDenied)
	total, _, err := scanEC2(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanEC2_StoresIpamExtendedRows(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanEC2TopLevel(t, st, ipxTopLevelHandlers(&calls))
	if err != nil {
		t.Fatalf("scanEC2: %v", err)
	}
	// 1 IPAM + 2 policies + 2 BYOIP + 2 BYOASN + 2 allocation documents of policy 1
	if total != 9 {
		t.Errorf("total = %d, want 9", total)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{ipxPolicy1}) {
		t.Errorf("policies queried = %v, want only the live %s", got, ipxPolicy1)
	}
	for typ, want := range map[string]int{TypeEC2ByoipCidr: 2, TypeEC2IpamByoasn: 2, TypeEC2IpamPolicyAllocationRule: 2} {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d, want %d", typ, n, want)
		}
	}
	assertContainsNative(t, st, ipxPolicyARN(ipxPolicy1), ipxRuleID(ipxPolicy1, "eip", ipxLocale))
}

// The extended phases run after every older category: a hard failure of
// either one leaves an older category's slow call uncancelled, and an older
// category's failure does not skip them.
func TestScanEC2_IpamExtendedPhasesAfterOlderCategories(t *testing.T) {
	for _, failOp := range []string{"DescribeByoipCidrs", "DescribeIpamByoasn", "GetIpamPolicyAllocationRules"} {
		t.Run(failOp+" fails", func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			h := ipxTopLevelHandlers(&calls)
			h[failOp] = func(any) (any, error) { return nil, errIpxTest }
			acct := newTestAccount(testAccountID)
			ctxHandlers := ignoreCtx(h)
			// DescribeIpams answers only after a pause, and gives up if its
			// context is cancelled first, as it would be by a sibling failure.
			ctxHandlers["DescribeIpams"] = func(ctx context.Context, in any) (any, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
					return h["DescribeIpams"](in)
				}
			}
			acct.cfg = opStubConfigCtx(ctxHandlers, ipxDenied)
			_, _, err := scanEC2(context.Background(), acct, testRegion, st, testScanID)
			if !errors.Is(err, errIpxTest) {
				t.Fatalf("err = %v, want the %s error", err, failOp)
			}
			if _, ok := rowsByNativeID(t, st, TypeEC2IPAM)[ipxIpamARN]; !ok {
				t.Errorf("IPAM row missing: the %s failure cancelled an older category", failOp)
			}
		})
	}
	t.Run("older category fails", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		h := ipxTopLevelHandlers(&calls)
		h["DescribeIpams"] = func(any) (any, error) { return nil, errIpxTest }
		_, err := scanEC2TopLevel(t, st, h)
		if !errors.Is(err, errIpxTest) {
			t.Fatalf("err = %v, want the DescribeIpams error", err)
		}
		if n := len(rowsByNativeID(t, st, TypeEC2ByoipCidr)); n != 2 {
			t.Errorf("BYOIP rows = %d, want 2", n)
		}
		// With the IPAM listing failed no home IPAM was stored, so BYOASN is
		// not listed.
		if n := len(rowsByNativeID(t, st, TypeEC2IpamByoasn)); n != 0 {
			t.Errorf("BYOASN rows = %d, want 0", n)
		}
	})
}

// A transient error on either side of the join is demoted to one warning, so
// it cannot make the dispatcher read the other side's hard error as transient.
func TestScanEC2_TransientSideDoesNotMaskHardError(t *testing.T) {
	cases := []struct {
		name, transientOp, hardOp, warnOp string
	}{
		{"transient older category", "DescribeIpams", "DescribeByoipCidrs", "ec2"},
		{"transient byoip", "DescribeByoipCidrs", "DescribeIpams", ec2ByoipCidrsOp},
		{"transient byoasn", "DescribeIpamByoasn", "DescribeByoipCidrs", ec2IpamByoasnOp},
		{"transient allocation rules", "GetIpamPolicyAllocationRules", "DescribeByoipCidrs", ec2IpamPolicyAllocationRulesOp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			h := ipxTopLevelHandlers(&calls)
			h[c.transientOp] = func(any) (any, error) { return nil, io.EOF }
			h[c.hardOp] = func(any) (any, error) { return nil, errIpxTest }
			warnings := countOpWarnings(st, c.warnOp)
			_, err := scanEC2TopLevel(t, st, h)
			if !errors.Is(err, errIpxTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v, want outcomeError", got)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.warnOp, *warnings)
			}
		})
	}
}

// DescribeIpamByoasn is called only in a Region that is home to an IPAM this
// scan stored.
func TestScanEC2_ByoasnOnlyInIpamHomeRegion(t *testing.T) {
	cases := []struct {
		name     string
		ipams    []ec2types.Ipam
		wantCall bool
	}{
		{"home IPAM", []ec2types.Ipam{
			{IpamId: ptrStr("ipam-0other"), IpamArn: ptrStr("arn:aws:ec2::123456789012:ipam/ipam-0other"), IpamRegion: ptrStr("eu-west-1")},
			{IpamId: ptrStr(ipxIpamID), IpamArn: ptrStr(ipxIpamARN), IpamRegion: ptrStr(testRegion)},
		}, true},
		{"IPAM homed elsewhere", []ec2types.Ipam{
			{IpamId: ptrStr(ipxIpamID), IpamArn: ptrStr(ipxIpamARN), IpamRegion: ptrStr("eu-west-1")},
		}, false},
		{"no IPAM", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls, byoasnCalls parentCalls
			h := ipxTopLevelHandlers(&calls)
			h["DescribeIpams"] = func(any) (any, error) { return &ec2.DescribeIpamsOutput{Ipams: c.ipams}, nil }
			h["DescribeIpamByoasn"] = func(in any) (any, error) {
				byoasnCalls.add("call")
				return ipxByoasnHandler(in)
			}
			if _, err := scanEC2TopLevel(t, st, h); err != nil {
				t.Fatalf("scanEC2: %v", err)
			}
			if called := len(byoasnCalls.distinct()) > 0; called != c.wantCall {
				t.Errorf("DescribeIpamByoasn called = %v, want %v", called, c.wantCall)
			}
			wantRows := 0
			if c.wantCall {
				wantRows = 2
			}
			if n := len(rowsByNativeID(t, st, TypeEC2IpamByoasn)); n != wantRows {
				t.Errorf("BYOASN rows = %d, want %d", n, wantRows)
			}
		})
	}
}

// A repeated NextToken ends a policy's allocation-rule loop instead of spinning.
func TestScanEC2IpamPolicyAllocationRules_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	ipxSeedPolicies(t, st, ipxPolicy1)
	calls := 0
	_, _, err := scanEC2IpamPolicyAllocationRules(context.Background(), ipxClient(map[string]func(any) (any, error){
		"GetIpamPolicyAllocationRules": func(any) (any, error) {
			calls++
			if calls > 3 {
				return nil, errIpxTest
			}
			return &ec2.GetIpamPolicyAllocationRulesOutput{NextToken: ptrStr(ipxPageNext), IpamPolicyDocuments: []ec2types.IpamPolicyDocument{{
				IpamPolicyId: ptrStr(ipxPolicy1), ResourceType: ec2types.IpamPolicyResourceTypeEip, Locale: ptrStr(ipxLocale),
			}}}, nil
		},
	}), newTestAccount(testAccountID), testRegion, st, testScanID, ipxParents(ipxPolicy1))
	if err != nil {
		t.Fatalf("scanEC2IpamPolicyAllocationRules: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (stop when the token repeats)", calls)
	}
}
