package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehub"
	rhtypes "github.com/aws/aws-sdk-go-v2/service/resiliencehub/types"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehubv2"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeResilienceHubApp, Service: "resilience-hub"})
	registerType(restype.Descriptor{Type: TypeResilienceHubResiliencyPolicy, Service: "resilience-hub"})
	registerType(restype.Descriptor{Type: TypeResilienceHubAppAssessment, Service: "resilience-hub"})
	registerType(restype.Descriptor{Type: TypeResilienceHubRecommendationTemplate, Service: "resilience-hub"})
	registerType(restype.Descriptor{Type: TypeResilienceHubAppInputSource, Service: "resilience-hub"})
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
	ListAppInputSources(context.Context, *resiliencehub.ListAppInputSourcesInput, ...func(*resiliencehub.Options)) (*resiliencehub.ListAppInputSourcesOutput, error)
}

// scanResilienceHub discovers Resilience Hub resources from the v1 (app) and
// v2 (service/system) APIs, then the input sources of every app and the
// functions and input sources of every service those phases stored. The
// fan-outs run whatever the earlier phases returned, so their failure costs
// those phases nothing. Every side's transient error is demoted to a warning, since one
// transient member would make the dispatcher read the whole join as
// transient; the counts are summed.
func scanResilienceHub(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := resiliencehub.NewFromConfig(acct.cfg, func(o *resiliencehub.Options) { o.Region = region })
	v2 := resiliencehubv2.NewFromConfig(acct.cfg, func(o *resiliencehubv2.Options) { o.Region = region })
	parents, total, inserted, err := scanRHPhases(ctx, client, v2, acct, region, st, scanID)
	t1, i1, serr := scanRHAppInputSources(ctx, client, acct, region, st, scanID, parents.appARNs)
	t2, i2, ferr := scanRHServiceFunctions(ctx, v2, acct, region, st, scanID, parents.serviceARNs)
	t3, i3, ierr := scanRHInputSources(ctx, v2, acct, region, st, scanID, parents.serviceARNs)
	return total + t1 + t2 + t3, inserted + i1 + i2 + i3, errors.Join(
		demoteTransient(st, "resilience-hub", acct, region, err),
		demoteTransient(st, rhListAppInputSourcesOp, acct, region, serr),
		demoteTransient(st, rhListServiceFunctionsOp, acct, region, ferr),
		demoteTransient(st, rhListInputSourcesOp, acct, region, ierr),
	)
}

// rhStoredParents holds the apps and v2 services scanRHPhases upserted this
// scan: the parents of the fan-outs that follow it.
type rhStoredParents struct{ appARNs, serviceARNs []string }

// scanRHPhases runs the v1 phases, then the v2 ones, stopping at the first
// error, and returns the parents stored before it.
func scanRHPhases(ctx context.Context, client resilienceHubAPI, v2 resilienceHubV2API, acct *account, region string, st *store.Store, scanID string) (parents rhStoredParents, total, inserted int, err error) {
	appARNs, t, i, ferr := scanRHApps(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return parents, total, inserted, ferr
	}
	parents.appARNs = appARNs
	total += t
	inserted += i

	t, i, ferr = scanRHResiliencyPolicies(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return parents, total, inserted, ferr
	}
	total += t
	inserted += i

	assessmentARNs, t, i, ferr := scanRHAppAssessments(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return parents, total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanRHRecommendationTemplates(ctx, client, acct, region, st, scanID, assessmentARNs)
	if ferr != nil {
		return parents, total, inserted, ferr
	}
	total += t
	inserted += i

	// Last, so a v2 failure cannot cost the v1 phases' rows.
	parents.serviceARNs, t, i, ferr = scanResilienceHubV2(ctx, v2, acct, region, st, scanID)
	total += t
	inserted += i
	return parents, total, inserted, ferr
}

// scanRHApps returns the apps' ARNs once they are stored: the parents
// ListAppInputSources requires.
func scanRHApps(ctx context.Context, client resilienceHubAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var appARNs []string
	var nextToken *string
	for {
		out, err := client.ListApps(ctx, &resiliencehub.ListAppsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "resiliencehub:ListApps", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("resiliencehub:ListApps: %w", err)
		}
		for _, a := range out.AppSummaries {
			arn := sv(a.AppArn)
			if arn == "" {
				continue
			}
			appARNs = append(appARNs, arn)
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
	t, i, err := upsertBatch(st, batch, "resilience-hub apps")
	if err != nil {
		return nil, t, i, err
	}
	return appARNs, t, i, nil
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

const (
	rhListAppInputSourcesOp = "resiliencehub:ListAppInputSources"
	// rhPageLimit is the model's MaxResults cap (range 1..100) for
	// ListAppInputSources, ListServiceFunctions and ListInputSources.
	rhPageLimit int32 = 100
	// rhInputSourceAppVersion is the app version whose input sources are
	// listed. Imports write input sources into the draft
	// (ImportResourcesToDraftAppVersion) and DeleteAppInputSource takes no
	// version, so the draft holds an app's current input sources; a published
	// version exists only after PublishAppVersion. The SDK model does not
	// enumerate version names: "draft" is the name AWS's user guide and CLI
	// examples use, unverified against a live account.
	rhInputSourceAppVersion = "draft"
)

// scanRHAppInputSources lists the input sources of each app's draft version.
// An input source has no ARN; its NativeID is {appARN}/input-source/{key}
// (rhInputSourceKey).
func scanRHAppInputSources(ctx context.Context, client resilienceHubAPI, acct *account, region string, st *store.Store, scanID string, appARNs []string) (int, int, error) {
	return childFanOut(ctx, st, acct, region, rhListAppInputSourcesOp, rhParents(appARNs), isRHParentGone,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			appARN := p.arn
			var rows []*store.Resource
			pager := resiliencehub.NewListAppInputSourcesPaginator(client, &resiliencehub.ListAppInputSourcesInput{
				AppArn: &appARN, AppVersion: sdkaws.String(rhInputSourceAppVersion), MaxResults: sdkaws.Int32(rhPageLimit),
			})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, src := range out.AppInputSources {
					key := rhInputSourceKey(src)
					if key == "" {
						continue
					}
					rows = append(rows, rhResource(acct, region, scanID, TypeResilienceHubAppInputSource,
						appARN+"/input-source/"+key, sv(src.SourceName), "", nil, src))
				}
			}
			return rows, nil
		})
}

// rhInputSourceKey is the field DeleteAppInputSource identifies an input
// source by, chosen by its model-required ImportType: the cluster ARN and
// namespace for EKS (one source per namespace, so a shared cluster SourceArn
// would collide), the state file URL for Terraform, and the source ARN for
// every other type. "" when the chosen field is missing.
func rhInputSourceKey(src rhtypes.AppInputSource) string {
	switch src.ImportType {
	case rhtypes.ResourceMappingTypeEks:
		eks := src.EksSourceClusterNamespace
		if eks == nil || sv(eks.EksClusterArn) == "" || sv(eks.Namespace) == "" {
			return ""
		}
		return sv(eks.EksClusterArn) + "/" + sv(eks.Namespace)
	case rhtypes.ResourceMappingTypeTerraform:
		if src.TerraformSource == nil {
			return ""
		}
		return sv(src.TerraformSource.S3StateFileUrl)
	default:
		return sv(src.SourceArn)
	}
}
