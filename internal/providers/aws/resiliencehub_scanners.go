package aws

import (
	"context"

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
	ListTagsForResource(context.Context, *resiliencehub.ListTagsForResourceInput, ...func(*resiliencehub.Options)) (*resiliencehub.ListTagsForResourceOutput, error)
}

const (
	rhListAppsOp                    = "resiliencehub:ListApps"
	rhListResiliencyPoliciesOp      = "resiliencehub:ListResiliencyPolicies"
	rhListAppAssessmentsOp          = "resiliencehub:ListAppAssessments"
	rhListRecommendationTemplatesOp = "resiliencehub:ListRecommendationTemplates"
	// rhTagsOp is the one IAM action that both SDK clients' ListTagsForResource
	// call, so their tag denials are one warning.
	rhTagsOp = "resiliencehub:ListTagsForResource"
)

// rhNotFound is the not-found code both ListTagsForResource models list: the
// resource was deleted after it was listed.
var rhNotFound = []string{"ResourceNotFoundException"}

// scanResilienceHub discovers Resilience Hub resources from the v1 (app) and
// v2 (service/system) APIs, then the input sources of every app and the
// functions and input sources of every service those phases stored. Every
// listing runs as its own phase, one at a time and in that order: a phase's
// error does not stop the later ones; a failed write and ctx's end do, and so
// does a first phase that stores nothing and fails only with NXDOMAIN, since
// the region does not serve the service (runHostPhases). Both APIs are
// served on the resiliencehub endpoint, so every phase is on one host. A
// region where only v1 is deployed skips the v2 phases (scanRHPolicies).
// Apps, resiliency policies, app assessments, recommendation templates, v2
// policies, services and systems are stored with their tags; both clients'
// tag denials are one warning for the scan.
func scanResilienceHub(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := resiliencehub.NewFromConfig(acct.cfg, func(o *resiliencehub.Options) { o.Region = region })
	v2 := resiliencehubv2.NewFromConfig(acct.cfg, func(o *resiliencehubv2.Options) { o.Region = region })
	tags := rhTags(client).sharingDenials()
	v2Tags := rhTagsV2(v2)
	v2Tags.denials = tags.denials

	var (
		apps, assessments, services, systems []childParent
		v2Absent                             bool
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "resilience-hub", 0, []hostPhase{
		{op: rhListAppsOp, run: func(p *phaseTotals) (failed bool) {
			apps, failed = scanRHApps(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, rhListResiliencyPoliciesOp, func() (int, int, error) {
			return scanRHResiliencyPolicies(ctx, client, acct, region, st, scanID)
		}),
		{op: rhListAppAssessmentsOp, run: func(p *phaseTotals) (failed bool) {
			assessments, failed = scanRHAppAssessments(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, rhListRecommendationTemplatesOp, func() (int, int, error) {
			return scanRHRecommendationTemplates(ctx, client, acct, region, st, scanID, assessments)
		}),
		{op: rhListPoliciesOp, run: func(p *phaseTotals) (failed bool) {
			v2Absent, failed = scanRHPolicies(ctx, v2, acct, region, st, scanID, v2Tags, p)
			return failed
		}},
		// The v2 child phases further down need no gate: with the v2 API
		// absent they get no services or systems, so they make no call.
		{op: rhListServicesOp, run: func(p *phaseTotals) (failed bool) {
			if v2Absent {
				return false
			}
			services, failed = scanRHServices(ctx, v2, acct, region, st, scanID, v2Tags, p)
			return failed
		}},
		{op: rhListSystemsOp, run: func(p *phaseTotals) (failed bool) {
			if v2Absent {
				return false
			}
			systems, failed = scanRHSystems(ctx, v2, acct, region, st, scanID, v2Tags, p)
			return failed
		}},
		countedPhase(0, rhListTestsOp, func() (int, int, error) {
			return scanRHTests(ctx, v2, acct, region, st, scanID, childParentIDs(services))
		}),
		countedPhase(0, rhListUserJourneysOp, func() (int, int, error) {
			return scanRHUserJourneys(ctx, v2, acct, region, st, scanID, childParentIDs(systems))
		}),
		countedPhase(0, rhListAppInputSourcesOp, func() (int, int, error) {
			return scanRHAppInputSources(ctx, client, acct, region, st, scanID, childParentIDs(apps))
		}),
		countedPhase(0, rhListServiceFunctionsOp, func() (int, int, error) {
			return scanRHServiceFunctions(ctx, v2, acct, region, st, scanID, childParentIDs(services))
		}),
		countedPhase(0, rhListInputSourcesOp, func() (int, int, error) {
			return scanRHInputSources(ctx, v2, acct, region, st, scanID, childParentIDs(services))
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// rhTags reads a v1 row's tags with ListTagsForResource, which the Service
// Reference lists for every Resilience Hub type. It is read for applications
// and app assessments, whose listed elements carry no tags; resiliency
// policies and recommendation templates carry Tags in their listed element.
// It is sent the row's NativeID, the ARN its listing returned; the op takes
// no page token and returns every tag at once.
func rhTags(client resilienceHubAPI) rowTagReader {
	return rowTagReader{
		op:       rhTagsOp,
		notFound: rhNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &resiliencehub.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// rhRow is the rhResource of a v1 listed element stored under its ARN, named
// name, with tags (nil for none or not yet read); an element without an ARN
// is not stored (arnRow), and what names such elements in the listing's
// warning.
func rhRow(acct *account, region, scanID, typ, what string, arn, name *string, status string, tags *string, element any) (*store.Resource, string, string) {
	r := rhResource(acct, region, scanID, typ, sv(arn), sv(name), status, nil, element)
	r.TagsJSON = tags
	return arnRow(what, arn, name, r)
}

// scanRHApps stores the apps, each page written as soon as its rows' tags
// are read, and returns the stored ones keyed by their ARN: the parents
// ListAppInputSources requires. It reports whether a write failed, after
// which the scan must stop.
func scanRHApps(ctx context.Context, client resilienceHubAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, rhListAppsOp, "apps", "AppArn",
		func(ctx context.Context, token *string) ([]rhtypes.AppSummary, *string, error) {
			out, err := client.ListApps(ctx, &resiliencehub.ListAppsInput{MaxResults: sdkaws.Int32(rhPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AppSummaries, out.NextToken, nil
		},
		func(a rhtypes.AppSummary) (*store.Resource, string, string) {
			return rhRow(acct, region, scanID, TypeResilienceHubApp, "apps", a.AppArn, a.Name, string(a.Status), nil, a)
		},
		func(a rhtypes.AppSummary) string { return sv(a.AppArn) },
		tags, p)
}

// scanRHResiliencyPolicies stores the resiliency policies with the tags their
// listed element carries, so no tag op is called for them.
// ListResiliencyPolicies lists the account's own policies; the AWS-suggested
// ones come from a separate op (ListSuggestedResiliencyPolicies), which is
// not called.
func scanRHResiliencyPolicies(ctx context.Context, client resilienceHubAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, rhListResiliencyPoliciesOp,
		func(ctx context.Context, token *string) ([]rhtypes.ResiliencyPolicy, *string, error) {
			out, err := client.ListResiliencyPolicies(ctx, &resiliencehub.ListResiliencyPoliciesInput{MaxResults: sdkaws.Int32(rhPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResiliencyPolicies, out.NextToken, nil
		},
		func(pol rhtypes.ResiliencyPolicy) (*store.Resource, string, string) {
			return rhRow(acct, region, scanID, TypeResilienceHubResiliencyPolicy, "resiliency policies", pol.PolicyArn, pol.PolicyName, "", mapTagsJSON(pol.Tags), pol)
		})
}

// scanRHAppAssessments stores the app assessments, each page written as soon
// as its rows' tags are read: every assessment run adds one. It returns the
// stored ones keyed by their ARN: the parents scanRHRecommendationTemplates
// requires. It reports whether a write failed, after which the scan must
// stop.
func scanRHAppAssessments(ctx context.Context, client resilienceHubAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, rhListAppAssessmentsOp, "app assessments", "AssessmentArn",
		func(ctx context.Context, token *string) ([]rhtypes.AppAssessmentSummary, *string, error) {
			out, err := client.ListAppAssessments(ctx, &resiliencehub.ListAppAssessmentsInput{MaxResults: sdkaws.Int32(rhPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AssessmentSummaries, out.NextToken, nil
		},
		func(a rhtypes.AppAssessmentSummary) (*store.Resource, string, string) {
			return rhRow(acct, region, scanID, TypeResilienceHubAppAssessment, "app assessments", a.AssessmentArn, a.AssessmentName, string(a.AssessmentStatus), nil, a)
		},
		func(a rhtypes.AppAssessmentSummary) string { return sv(a.AssessmentArn) },
		tags, p)
}

// scanRHRecommendationTemplates lists the templates of each assessment the
// assessment phase stored, the assessments read concurrently, and stores them
// with the tags their listed element carries, so no tag op is called for
// them. ListRecommendationTemplates requires assessmentArn server-side (the
// SDK validator does not enforce it): an empty input fails with a garbled
// "explicit deny on resource: *", so no assessment means no call. The op
// models no not-found error, so an assessment deleted since it was listed
// fails its listing with whatever the service answers, and that error is
// returned.
func scanRHRecommendationTemplates(ctx context.Context, client resilienceHubAPI, acct *account, region string, st *store.Store, scanID string, assessments []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, rhListRecommendationTemplatesOp, nil, childParentIDs(assessments),
		func(ctx context.Context, assessmentARN string, token *string) ([]rhtypes.RecommendationTemplate, *string, error) {
			out, err := client.ListRecommendationTemplates(ctx, &resiliencehub.ListRecommendationTemplatesInput{
				AssessmentArn: &assessmentARN, MaxResults: sdkaws.Int32(rhPageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.RecommendationTemplates, out.NextToken, nil
		},
		func(_ string, rt rhtypes.RecommendationTemplate) (*store.Resource, string, string) {
			return rhRow(acct, region, scanID, TypeResilienceHubRecommendationTemplate, "recommendation templates", rt.RecommendationTemplateArn, rt.Name, "", mapTagsJSON(rt.Tags), rt)
		})
}

const (
	rhListAppInputSourcesOp = "resiliencehub:ListAppInputSources"
	// rhPageLimit is the model's MaxResults cap (range 1..100) of every
	// Resilience Hub listing read here.
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
