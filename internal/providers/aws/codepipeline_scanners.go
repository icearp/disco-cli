package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codepipeline"
	"github.com/aws/aws-sdk-go-v2/service/codepipeline/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// codePipelineARN builds arn:{p}:codepipeline:{region}:{acct}:{resource}. A
// pipeline's resource is its bare name, so the resolver rebuilds a target
// pipeline from the name another pipeline's trigger names.
func codePipelineARN(partition, region, acct, resource string) string {
	return arnFor(partition, "codepipeline", region, acct, resource)
}

func init() {
	registerType(restype.Descriptor{Type: TypeCodePipelinePipeline, Service: "codepipeline"})
	registerType(restype.Descriptor{Type: TypeCodePipelineCustomActionType, Service: "codepipeline"})
	// A POST to a webhook's Url starts its pipeline when the request passes
	// the webhook's authentication, and UNAUTHENTICATED accepts every request,
	// so the Url is a credential. Its SecretToken needs no rule: the SDK
	// documents that responses redact it.
	registerType(restype.Descriptor{Type: TypeCodePipelineWebhook, Service: "codepipeline", Redact: []redact.Rule{{Path: "Url", Mode: redact.RedactScalar}}})
	registerService(serviceEntry{
		name: "aws:codepipeline",
		fn:   scanCodePipeline,
	})
}

const (
	cplListPipelinesOp   = "codepipeline:ListPipelines"
	cplGetPipelineOp     = "codepipeline:GetPipeline"
	cplListActionTypesOp = "codepipeline:ListActionTypes"
	cplListWebhooksOp    = "codepipeline:ListWebhooks"
	cplListTagsOp        = "codepipeline:ListTagsForResource"

	// cplPipelinePageCap is ListPipelines' maxResults maximum (MaxPipelines,
	// range 1-1000); cplPageCap is ListWebhooks' and ListTagsForResource's
	// (MaxResults, range 1-100). ListActionTypes models no page size.
	cplPipelinePageCap = 1000
	cplPageCap         = 100

	// cplNoActionTypeID is why a listed action type is not stored.
	cplNoActionTypeID = "custom action types lack a category, provider or version"
)

var (
	// cplPipelineGone is the not-found code GetPipeline models, for a pipeline
	// deleted since it was listed.
	cplPipelineGone = []string{"PipelineNotFoundException"}
	// cplTagsGone is the not-found code ListTagsForResource models.
	cplTagsGone = []string{"ResourceNotFoundException"}
)

type codePipelineAPI interface {
	ListPipelines(context.Context, *codepipeline.ListPipelinesInput, ...func(*codepipeline.Options)) (*codepipeline.ListPipelinesOutput, error)
	ListActionTypes(context.Context, *codepipeline.ListActionTypesInput, ...func(*codepipeline.Options)) (*codepipeline.ListActionTypesOutput, error)
	ListWebhooks(context.Context, *codepipeline.ListWebhooksInput, ...func(*codepipeline.Options)) (*codepipeline.ListWebhooksOutput, error)
	GetPipeline(context.Context, *codepipeline.GetPipelineInput, ...func(*codepipeline.Options)) (*codepipeline.GetPipelineOutput, error)
	ListTagsForResource(context.Context, *codepipeline.ListTagsForResourceInput, ...func(*codepipeline.Options)) (*codepipeline.ListTagsForResourceOutput, error)
}

func scanCodePipeline(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := codepipeline.NewFromConfig(acct.cfg, func(o *codepipeline.Options) { o.Region = region })
	return scanCodePipelineAll(ctx, client, acct, region, st, scanID)
}

// scanCodePipelineAll discovers pipelines, Owner=Custom action types
// (AWS-owned and third-party action types are catalogue, not customer
// resources, so they are not listed) and webhooks, one phase at a time. A
// phase's error does not stop the later ones; a failed write and ctx's end
// do, and so does an NXDOMAIN of the first listing with nothing stored (the
// region does not serve the service) (runHostPhases). Pipelines and custom
// action types have their tags read per row (cplTags), whose denials are one
// warning for the scan; a webhook's tags come with its listed element.
func scanCodePipelineAll(ctx context.Context, client codePipelineAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := cplTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "codepipeline", 0, []hostPhase{
		{op: cplListPipelinesOp, run: func(p *phaseTotals) bool {
			return scanCPPipelines(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: cplListActionTypesOp, run: func(p *phaseTotals) bool {
			return scanCPCustomActionTypes(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, cplListWebhooksOp, func() (int, int, error) {
			return scanCPWebhooks(ctx, client, acct, region, st, scanID)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// cplTagKV is a CodePipeline tag's key and value.
func cplTagKV(t types.Tag) (*string, *string) { return t.Key, t.Value }

// cplTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the ARN in the Service Reference's format for the row's
// type: arn:{p}:codepipeline:{region}:{acct}:{PipelineName} for a pipeline
// and ...:actiontype:{Owner}/{Category}/{Provider}/{Version} for an action
// type. The Service Reference lists actiontype, pipeline and webhook under
// ListTagsForResource and TagResource; webhooks are not read here, their
// listed element carries their tags. The op pages its tags; every page is
// read, and a repeated nextToken fails the read. ResourceNotFoundException,
// its modeled not-found, drops a resource deleted after it was listed.
func cplTags(client codePipelineAPI) rowTagReader {
	return rowTagReader{
		op:       cplListTagsOp,
		notFound: cplTagsGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]types.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &codepipeline.ListTagsForResourceInput{
					ResourceArn: &arn, MaxResults: sdkaws.Int32(cplPageCap), NextToken: token,
				})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, cplTagKV), nil
		},
	}
}

// scanCPPipelines stores the pipelines with their tags, a page at a time.
// A pipeline's NativeID is built from its name (the summary carries no ARN).
// Each row's attributes are its GetPipeline output, which the resolver reads;
// a pipeline GetPipeline no longer finds (PipelineNotFoundException) was
// deleted after it was listed and is dropped, and any other failure keeps
// the listed summary as the row. It reports whether a write failed, after
// which the scan must stop.
func scanCPPipelines(ctx context.Context, client codePipelineAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	steps := tokenPageSteps{details: rowDetailReader{
		op: cplGetPipelineOp, what: "pipeline(s)", notFound: cplPipelineGone,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			out, err := client.GetPipeline(ctx, &codepipeline.GetPipelineInput{Name: r.Name})
			if err != nil {
				return nil, err
			}
			return out, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, region, cplListPipelinesOp,
		func(ctx context.Context, token *string) ([]types.PipelineSummary, *string, error) {
			out, err := client.ListPipelines(ctx, &codepipeline.ListPipelinesInput{MaxResults: sdkaws.Int32(cplPipelinePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Pipelines, out.NextToken, nil
		},
		func(s types.PipelineSummary) (*store.Resource, string, string) {
			name := sv(s.Name)
			if name == "" {
				return nil, "pipelines have no name", noIdentifier
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodePipelinePipeline, NativeID: codePipelineARN(acct.partition(), region, acct.ID, name),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}, "", ""
		},
		steps, tags, p)
}

// scanCPCustomActionTypes stores the Owner=Custom action types with their
// tags, a page at a time, each named {Category}/{Provider}/{Version}. The
// element carries no ARN, so the NativeID is built in the Service
// Reference's actiontype format,
// arn:{p}:codepipeline:{region}:{acct}:actiontype:Custom/{Category}/{Provider}/{Version}.
// It reports whether a write failed, after which the scan must stop.
func scanCPCustomActionTypes(ctx context.Context, client codePipelineAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	owner := types.ActionOwnerCustom
	return storeTokenPagesTags(ctx, st, acct, region, cplListActionTypesOp,
		func(ctx context.Context, token *string) ([]types.ActionType, *string, error) {
			out, err := client.ListActionTypes(ctx, &codepipeline.ListActionTypesInput{ActionOwnerFilter: owner, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ActionTypes, out.NextToken, nil
		},
		func(a types.ActionType) (*store.Resource, string, string) {
			if a.Id == nil {
				return nil, cplNoActionTypeID, noIdentifier
			}
			category, provider, version := string(a.Id.Category), sv(a.Id.Provider), sv(a.Id.Version)
			label := category + "/" + provider + "/" + version
			if category == "" || provider == "" || version == "" {
				return nil, cplNoActionTypeID, label
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeCodePipelineCustomActionType,
				NativeID: codePipelineARN(acct.partition(), region, acct.ID, "actiontype:"+string(owner)+"/"+label),
				Name:     &label, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
}

// scanCPWebhooks stores the webhooks under their listed ARN, named by their
// definition's name, with the tags their listed element carries.
func scanCPWebhooks(ctx context.Context, client codePipelineAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, cplListWebhooksOp,
		func(ctx context.Context, token *string) ([]types.ListWebhookItem, *string, error) {
			out, err := client.ListWebhooks(ctx, &codepipeline.ListWebhooksInput{MaxResults: sdkaws.Int32(cplPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Webhooks, out.NextToken, nil
		},
		func(w types.ListWebhookItem) (*store.Resource, string, string) {
			var name *string
			if w.Definition != nil {
				name = w.Definition.Name
			}
			return arnRow("webhooks", w.Arn, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodePipelineWebhook, NativeID: sv(w.Arn),
				Name: name, Region: &region, TagsJSON: kvTagsJSON(w.Tags, cplTagKV),
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		})
}
