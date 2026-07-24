package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/xray"
)

func init() {
	registerType(restype.Descriptor{Type: TypeXRayGroup, Service: "xray", Leaf: true})
	registerType(restype.Descriptor{Type: TypeXRayResourcePolicy, Service: "xray", Leaf: true})
	registerType(restype.Descriptor{Type: TypeXRaySamplingRule, Service: "xray", Leaf: true})
	registerType(restype.Descriptor{Type: TypeXRayTransactionSearchConfig, Service: "xray", Leaf: true, Managed: true})
	registerService(serviceEntry{
		name: "aws:xray",
		fn:   scanXRay,
	})
}

type xrayAPI interface {
	GetGroups(context.Context, *xray.GetGroupsInput, ...func(*xray.Options)) (*xray.GetGroupsOutput, error)
	ListResourcePolicies(context.Context, *xray.ListResourcePoliciesInput, ...func(*xray.Options)) (*xray.ListResourcePoliciesOutput, error)
	GetSamplingRules(context.Context, *xray.GetSamplingRulesInput, ...func(*xray.Options)) (*xray.GetSamplingRulesOutput, error)
	GetTraceSegmentDestination(context.Context, *xray.GetTraceSegmentDestinationInput, ...func(*xray.Options)) (*xray.GetTraceSegmentDestinationOutput, error)
}

// scanXRay discovers X-Ray groups, resource policies, sampling rules, and the
// per-(account,region) Transaction Search configuration.
//
// AWS::XRay::TransactionSearchConfig has no list/dedicated Get endpoint;
// GetTraceSegmentDestination is the singleton that backs it (controls whether
// segments index to CloudWatch Logs for Transaction Search).
func scanXRay(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := xray.NewFromConfig(acct.cfg, func(o *xray.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanXRayGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanXRayResourcePolicies(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanXRaySamplingRules(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanXRayTransactionSearchConfig(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanXRayGroups(ctx context.Context, client xrayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := xray.NewGetGroupsPaginator(client, &xray.GetGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "xray:GetGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("xray:GetGroups: %w", err)
		}
		for _, g := range out.Groups {
			arn := sv(g.GroupARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeXRayGroup, NativeID: arn,
				Name: g.GroupName, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
				// GroupName "Default" = AWS-managed default trace group present in every account.
				ManagedByProvider: sv(g.GroupName) == "Default",
			})
		}
	}
	return upsertBatch(st, batch, "xray groups")
}

// scanXRayResourcePolicies synthesizes ARN per policy: arn:aws:xray:{r}:{a}:resource-policy/{name}.
func scanXRayResourcePolicies(ctx context.Context, client xrayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := xray.NewListResourcePoliciesPaginator(client, &xray.ListResourcePoliciesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "xray:ListResourcePolicies", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("xray:ListResourcePolicies: %w", err)
		}
		for _, p := range out.ResourcePolicies {
			name := sv(p.PolicyName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:xray:%s:%s:resource-policy/%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeXRayResourcePolicy, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "xray resource-policies")
}

func scanXRaySamplingRules(ctx context.Context, client xrayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := xray.NewGetSamplingRulesPaginator(client, &xray.GetSamplingRulesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "xray:GetSamplingRules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("xray:GetSamplingRules: %w", err)
		}
		for _, r := range out.SamplingRuleRecords {
			if r.SamplingRule == nil {
				continue
			}
			arn := sv(r.SamplingRule.RuleARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeXRaySamplingRule, NativeID: arn,
				Name: r.SamplingRule.RuleName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
				// RuleName "Default" = AWS-managed default sampling rule present in every account.
				ManagedByProvider: sv(r.SamplingRule.RuleName) == "Default",
			})
		}
	}
	return upsertBatch(st, batch, "xray sampling-rules")
}

// scanXRayTransactionSearchConfig captures the per-(account,region)
// trace-segment-destination singleton backing X-Ray Transaction Search. No
// ARN — synthesize one. ManagedByProvider=true: account/region config, not a
// user-created resource.
func scanXRayTransactionSearchConfig(ctx context.Context, client xrayAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetTraceSegmentDestination(ctx, &xray.GetTraceSegmentDestinationInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "xray:GetTraceSegmentDestination", acct.ID, region, err)
		}
		if isAPIErrorCode(err, "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("xray:GetTraceSegmentDestination: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:xray:%s:%s:transaction-search-config", region, acct.ID)
	label := "transaction-search-config"
	status := string(out.Status)
	return upsertBatch(st, []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeXRayTransactionSearchConfig, NativeID: arn,
		Name: &label, Region: &region, Status: &status,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}}, "xray transaction-search-config")
}
