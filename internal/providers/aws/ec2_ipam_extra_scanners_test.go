package aws

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/store"
)

// Both IPAM-extra listings end on a repeated NextToken with
// errRepeatedNextToken instead of looping or reading as complete, and the row
// already listed is stored.
func TestScanEC2IPAMExtra_RepeatedTokenFails(t *testing.T) {
	policyARN := ec2ARN(testRegion, testAccountID, "ipam-policy", "ipam-policy-1")
	tokenARN := ec2ARN(testRegion, testAccountID, "ipam-external-resource-verification-token", "ipam-ext-res-ver-token-1")
	for _, tc := range []struct {
		name, sdkOp, typ, nativeID string
		page                       any
		run                        func(ec2API, *store.Store) error
	}{
		{
			name: "policies", sdkOp: "DescribeIpamPolicies", typ: TypeEC2IpamPolicy, nativeID: policyARN,
			page: &ec2.DescribeIpamPoliciesOutput{NextToken: ptrStr("same"), IpamPolicies: []ec2types.IpamPolicy{
				{IpamPolicyArn: &policyARN, IpamPolicyId: ptrStr("ipam-policy-1")},
			}},
			run: func(c ec2API, st *store.Store) error {
				_, _, _, err := scanIpamPolicies(context.Background(), c, newTestAccount(testAccountID), testRegion, st, testScanID)
				return err
			},
		},
		{
			name: "verification tokens", sdkOp: "DescribeIpamExternalResourceVerificationTokens", typ: TypeEC2IpamExternalResourceVerificationToken, nativeID: tokenARN,
			page: &ec2.DescribeIpamExternalResourceVerificationTokensOutput{NextToken: ptrStr("same"), IpamExternalResourceVerificationTokens: []ec2types.IpamExternalResourceVerificationToken{
				{IpamExternalResourceVerificationTokenArn: &tokenARN},
			}},
			run: func(c ec2API, st *store.Store) error {
				_, _, err := scanIpamExternalResourceVerificationTokens(context.Background(), c, newTestAccount(testAccountID), testRegion, st, testScanID)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := 0
			client := ipxClient(map[string]func(any) (any, error){tc.sdkOp: func(any) (any, error) {
				calls++
				if calls > 3 {
					return nil, errIpxTest
				}
				return tc.page, nil
			}})
			if err := tc.run(client, st); !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v, want errRepeatedNextToken", err)
			}
			if calls != 2 {
				t.Errorf("calls = %d, want 2 (stop when the token repeats)", calls)
			}
			if _, ok := rowsByNativeID(t, st, tc.typ)[tc.nativeID]; !ok {
				t.Errorf("row %s not stored", tc.nativeID)
			}
		})
	}
}

// A repeated token in the IPAM policy listing fails scanEC2IPAMExtra but does
// not cancel the sibling verification-token listing, whose row is stored.
func TestScanEC2IPAMExtra_RepeatedPolicyTokenKeepsSibling(t *testing.T) {
	st := newTestStore(t)
	policyARN := ec2ARN(testRegion, testAccountID, "ipam-policy", "ipam-policy-1")
	tokenARN := ec2ARN(testRegion, testAccountID, "ipam-external-resource-verification-token", "ipam-ext-res-ver-token-1")
	w := &siblingWatch{failed: make(chan struct{})}
	var policyCalls atomic.Int32
	client := ec2.NewFromConfig(opStubConfigCtx(map[string]func(context.Context, any) (any, error){
		"DescribeIpamPolicies": func(context.Context, any) (any, error) {
			switch policyCalls.Add(1) {
			case 1:
			case 2:
				defer close(w.failed)
			default:
				return nil, errIpxTest
			}
			return &ec2.DescribeIpamPoliciesOutput{NextToken: ptrStr("same"), IpamPolicies: []ec2types.IpamPolicy{
				{IpamPolicyArn: &policyARN, IpamPolicyId: ptrStr("ipam-policy-1")},
			}}, nil
		},
		"DescribeIpamExternalResourceVerificationTokens": func(ctx context.Context, _ any) (any, error) {
			w.await(ctx)
			return &ec2.DescribeIpamExternalResourceVerificationTokensOutput{IpamExternalResourceVerificationTokens: []ec2types.IpamExternalResourceVerificationToken{
				{IpamExternalResourceVerificationTokenArn: &tokenARN},
			}}, nil
		},
	}, ipxDenied))
	_, _, _, err := scanEC2IPAMExtra(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if n := w.cancelled.Load(); n != 0 {
		t.Errorf("verification-token listing cancelled %d times; want 0", n)
	}
	for typ, id := range map[string]string{TypeEC2IpamPolicy: policyARN, TypeEC2IpamExternalResourceVerificationToken: tokenARN} {
		if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
			t.Errorf("%s %s not stored", typ, id)
		}
	}
}
