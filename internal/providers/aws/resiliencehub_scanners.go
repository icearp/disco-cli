package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehub"
)

func init() {
	registerService(serviceEntry{
		name: "aws:resilience-hub",
		fn:   scanResilienceHub,
		emits: []coverage.TypeDecl{
			{Service: "resilience-hub", DiscoType: TypeResilienceHubApp},
			{Service: "resilience-hub", DiscoType: TypeResilienceHubResiliencyPolicy},
		},
	})
}

type resilienceHubAPI interface {
	ListApps(context.Context, *resiliencehub.ListAppsInput, ...func(*resiliencehub.Options)) (*resiliencehub.ListAppsOutput, error)
	ListResiliencyPolicies(context.Context, *resiliencehub.ListResiliencyPoliciesInput, ...func(*resiliencehub.Options)) (*resiliencehub.ListResiliencyPoliciesOutput, error)
}

// scanResilienceHub discovers Resilience Hub applications and resiliency
// policies.
func scanResilienceHub(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := resiliencehub.NewFromConfig(acct.cfg, func(o *resiliencehub.Options) { o.Region = region })

	t, i, ferr := scanRHApps(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanRHResiliencyPolicies(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanRHApps(ctx context.Context, client resilienceHubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListApps(ctx, &resiliencehub.ListAppsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "resiliencehub:ListApps", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("resiliencehub:ListApps: %w", err)
		}
		for _, a := range out.AppSummaries {
			arn := sv(a.AppArn)
			if arn == "" {
				continue
			}
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeResilienceHubApp, NativeID: arn,
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "resilience-hub apps")
}

func scanRHResiliencyPolicies(ctx context.Context, client resilienceHubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListResiliencyPolicies(ctx, &resiliencehub.ListResiliencyPoliciesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "resiliencehub:ListResiliencyPolicies", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("resiliencehub:ListResiliencyPolicies: %w", err)
		}
		for _, p := range out.ResiliencyPolicies {
			arn := sv(p.PolicyArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeResilienceHubResiliencyPolicy, NativeID: arn,
				Name: p.PolicyName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "resilience-hub resiliency-policies")
}
