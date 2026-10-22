package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2IpamPolicy, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2IpamExternalResourceVerificationToken, Service: "ec2", Redact: []redact.Rule{{Path: "TokenValue", Mode: redact.RedactScalar}}})
}

// scanEC2IPAMExtra discovers IPAM policies and external-resource verification
// tokens. Neither op has an SDK paginator — manual NextToken loops. It also
// returns the stored policies whose allocation rules can still be read, for
// the allocation-rule phase.
func scanEC2IPAMExtra(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, policies []childParent, err error) {
	total, inserted, err = runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			t, n, p, err := scanIpamPolicies(ctx, client, acct, region, st, scanID)
			policies = p
			return t, n, err
		},
		func(ctx context.Context) (int, int, error) {
			return scanIpamExternalResourceVerificationTokens(ctx, client, acct, region, st, scanID)
		},
	)
	return total, inserted, policies, err
}

// scanIpamPolicies stores the IPAM policies and returns the stored policies
// whose allocation rules can still be read. It drops delete-complete ones,
// inferring (not observed) that a deleted policy has none.
func scanIpamPolicies(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, policies []childParent, err error) {
	var (
		batch   []*store.Resource
		token   *string
		pageErr error
	)
	for {
		out, perr := client.DescribeIpamPolicies(ctx, &ec2.DescribeIpamPoliciesInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, nil, skipIfAccessDenied(st, "ec2:DescribeIpamPolicies", acct.ID, region, perr)
			}
			return 0, 0, nil, fmt.Errorf("ec2:DescribeIpamPolicies: %w", perr)
		}
		for _, p := range out.IpamPolicies {
			arn := sv(p.IpamPolicyArn)
			if arn == "" {
				continue
			}
			status := string(p.State)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2IpamPolicy,
				NativeID:       arn,
				Region:         &region,
				Status:         &status,
				TagsJSON:       awsTagsJSON(p.Tags),
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
			if id := sv(p.IpamPolicyId); id != "" && p.State != ec2types.IpamPolicyStateDeleteComplete {
				policies = append(policies, childParent{id: id, arn: arn})
			}
		}
		var done bool
		if done, pageErr = lastPage(token, out.NextToken); done {
			break
		}
		token = out.NextToken
	}
	total, inserted, err = upsertListed(st, batch, "ec2:DescribeIpamPolicies", pageErr)
	if errors.Is(err, store.ErrStoreWrite) {
		return 0, 0, nil, err
	}
	return total, inserted, policies, err
}

func scanIpamExternalResourceVerificationTokens(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		batch   []*store.Resource
		token   *string
		pageErr error
	)
	for {
		out, perr := client.DescribeIpamExternalResourceVerificationTokens(ctx, &ec2.DescribeIpamExternalResourceVerificationTokensInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "ec2:DescribeIpamExternalResourceVerificationTokens", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("ec2:DescribeIpamExternalResourceVerificationTokens: %w", perr)
		}
		for _, vt := range out.IpamExternalResourceVerificationTokens {
			arn := sv(vt.IpamExternalResourceVerificationTokenArn)
			if arn == "" {
				continue
			}
			status := string(vt.State)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2IpamExternalResourceVerificationToken,
				NativeID:       arn,
				Name:           vt.TokenName,
				Region:         &region,
				Status:         &status,
				TagsJSON:       awsTagsJSON(vt.Tags),
				AttributesJSON: mustJSON(vt),
				DiscoveredBy:   scanID,
			})
		}
		var done bool
		if done, pageErr = lastPage(token, out.NextToken); done {
			break
		}
		token = out.NextToken
	}
	return upsertListed(st, batch, "ec2:DescribeIpamExternalResourceVerificationTokens", pageErr)
}
