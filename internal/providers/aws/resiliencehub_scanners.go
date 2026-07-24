package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehub"
)

func init() {
	registerType(restype.Descriptor{Type: TypeResilienceHubApp, Service: "resilience-hub", Upstream: "AWS::ResilienceHub::App", Leaf: true})
	registerType(restype.Descriptor{Type: TypeResilienceHubResiliencyPolicy, Service: "resilience-hub", Upstream: "AWS::ResilienceHub::ResiliencyPolicy", Leaf: true})
	registerType(restype.Descriptor{Type: TypeResilienceHubAppAssessment, Service: "resilience-hub", Upstream: "AWS::resiliencehub::app-assessment"})
	registerType(restype.Descriptor{Type: TypeResilienceHubRecommendationTemplate, Service: "resilience-hub", Upstream: "AWS::resiliencehub::recommendation-template", Leaf: true})
	registerService(serviceEntry{
		name: "aws:resilience-hub",
		fn:   scanResilienceHub,
	})
}

type resilienceHubAPI interface {
	ListApps(context.Context, *resiliencehub.ListAppsInput, ...func(*resiliencehub.Options)) (*resiliencehub.ListAppsOutput, error)
	ListResiliencyPolicies(context.Context, *resiliencehub.ListResiliencyPoliciesInput, ...func(*resiliencehub.Options)) (*resiliencehub.ListResiliencyPoliciesOutput, error)
	ListAppAssessments(context.Context, *resiliencehub.ListAppAssessmentsInput, ...func(*resiliencehub.Options)) (*resiliencehub.ListAppAssessmentsOutput, error)
	ListRecommendationTemplates(context.Context, *resiliencehub.ListRecommendationTemplatesInput, ...func(*resiliencehub.Options)) (*resiliencehub.ListRecommendationTemplatesOutput, error)
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

	assessmentARNs, t, i, ferr := scanRHAppAssessments(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanRHRecommendationTemplates(ctx, client, acct, region, st, scanID, assessmentARNs)
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

// scanRHAppAssessments upserts app-assessments and returns their ARNs — fan-out
// driver for scanRHRecommendationTemplates, whose ListRecommendationTemplates op
// requires an assessmentArn (server-required; SDK v2 validator omits it).
func scanRHAppAssessments(ctx context.Context, client resilienceHubAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := resiliencehub.NewListAppAssessmentsPaginator(client, &resiliencehub.ListAppAssessmentsInput{})
	var batch []*store.Resource
	var assessmentARNs []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "resiliencehub:ListAppAssessments", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("resiliencehub:ListAppAssessments: %w", err)
		}
		for _, a := range out.AssessmentSummaries {
			arn := sv(a.AssessmentArn)
			if arn == "" {
				continue
			}
			assessmentARNs = append(assessmentARNs, arn)
			status := string(a.AssessmentStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeResilienceHubAppAssessment, NativeID: arn,
				Name: a.AssessmentName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "resilience-hub app-assessments")
	return assessmentARNs, t, i, err
}

// scanRHRecommendationTemplates fans out per assessment ARN: ListRecommendationTemplates
// requires assessmentArn server-side (SDK v2 validator doesn't enforce it) — empty
// input fails with a garbled "explicit deny on resource: *". No assessmentARNs →
// zero API calls.
func scanRHRecommendationTemplates(ctx context.Context, client resilienceHubAPI, acct *account, region string, st *store.Store, scanID string, assessmentARNs []string) (int, int, error) {
	var batch []*store.Resource
	for _, assessmentARN := range assessmentARNs {
		pager := resiliencehub.NewListRecommendationTemplatesPaginator(client, &resiliencehub.ListRecommendationTemplatesInput{AssessmentArn: &assessmentARN})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				// Tolerate a per-assessment denial; keep scanning siblings.
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "resiliencehub:ListRecommendationTemplates", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("resiliencehub:ListRecommendationTemplates: %w", err)
			}
			for _, rt := range out.RecommendationTemplates {
				arn := sv(rt.RecommendationTemplateArn)
				if arn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeResilienceHubRecommendationTemplate, NativeID: arn,
					Name: rt.Name, Region: &region,
					AttributesJSON: mustJSON(rt), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "resilience-hub recommendation-templates")
}
