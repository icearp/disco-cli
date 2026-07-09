package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2IpamPolicy, Service: "ec2", Upstream: "AWS::ec2::ipam-policy"})
	registerType(restype.Descriptor{Type: TypeEC2IpamExternalResourceVerificationToken, Service: "ec2", Upstream: "AWS::ec2::ipam-external-resource-verification-token", Redact: []redact.Rule{{Path: "TokenValue", Mode: redact.RedactScalar}}})
}

// scanEC2IPAMExtra discovers IPAM policies and external-resource verification
// tokens. Neither op has an SDK paginator — manual NextToken loops.
func scanEC2IPAMExtra(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanIpamPolicies(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIpamExternalResourceVerificationTokens(ctx, client, acct, region, st, scanID)
		},
	)
}

func scanIpamPolicies(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var token *string
	for {
		out, perr := client.DescribeIpamPolicies(ctx, &ec2.DescribeIpamPoliciesInput{NextToken: token})
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "ec2:DescribeIpamPolicies", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("ec2:DescribeIpamPolicies: %w", perr)
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
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ec2 ipam-policies: %w", uerr)
	}
	return len(batch), n, nil
}

func scanIpamExternalResourceVerificationTokens(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var token *string
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
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert ec2 ipam-external-resource-verification-tokens: %w", uerr)
	}
	return len(batch), n, nil
}
