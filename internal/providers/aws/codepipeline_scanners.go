package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/codepipeline"
	"github.com/aws/aws-sdk-go-v2/service/codepipeline/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:codepipeline",
		fn:   scanCodePipeline,
		emits: []coverage.TypeDecl{
			{Service: "codepipeline", DiscoType: TypeCodePipelinePipeline},
			{Service: "codepipeline", DiscoType: TypeCodePipelineCustomActionType, Leaf: true},
			{Service: "codepipeline", DiscoType: TypeCodePipelineWebhook},
		},
	})
}

type codePipelineAPI interface {
	ListPipelines(context.Context, *codepipeline.ListPipelinesInput, ...func(*codepipeline.Options)) (*codepipeline.ListPipelinesOutput, error)
	ListActionTypes(context.Context, *codepipeline.ListActionTypesInput, ...func(*codepipeline.Options)) (*codepipeline.ListActionTypesOutput, error)
	ListWebhooks(context.Context, *codepipeline.ListWebhooksInput, ...func(*codepipeline.Options)) (*codepipeline.ListWebhooksOutput, error)
	GetPipeline(context.Context, *codepipeline.GetPipelineInput, ...func(*codepipeline.Options)) (*codepipeline.GetPipelineOutput, error)
}

// scanCodePipeline discovers pipelines, Owner=Custom action types (AWS-managed
// types are catalogue, not customer resources), and webhooks.
func scanCodePipeline(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codepipeline.NewFromConfig(acct.cfg, func(o *codepipeline.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanCPPipelines(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCPCustomActionTypes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanCPWebhooks(ctx, client, acct, region, st, scanID) },
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

func scanCPPipelines(ctx context.Context, client codePipelineAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListPipelines(ctx, &codepipeline.ListPipelinesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codepipeline:ListPipelines", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codepipeline:ListPipelines: %w", err)
		}
		for _, p := range out.Pipelines {
			name := sv(p.Name)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:codepipeline:%s:%s:%s", region, acct.ID, name)
			attrsJSON := mustJSON(p)
			if gout, gerr := client.GetPipeline(ctx, &codepipeline.GetPipelineInput{Name: p.Name}); gerr == nil {
				attrsJSON = mustJSON(gout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodePipelinePipeline, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "codepipeline pipelines")
}

func scanCPCustomActionTypes(ctx context.Context, client codePipelineAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	custom := types.ActionOwnerCustom
	var nextToken *string
	for {
		out, err := client.ListActionTypes(ctx, &codepipeline.ListActionTypesInput{
			ActionOwnerFilter: custom,
			NextToken:         nextToken,
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codepipeline:ListActionTypes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codepipeline:ListActionTypes: %w", err)
		}
		for _, a := range out.ActionTypes {
			if a.Id == nil {
				continue
			}
			category := string(a.Id.Category)
			provider := sv(a.Id.Provider)
			version := sv(a.Id.Version)
			if category == "" || provider == "" || version == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:codepipeline:%s:%s:actiontype/Custom/%s/%s/%s", region, acct.ID, category, provider, version)
			label := fmt.Sprintf("%s/%s/%s", category, provider, version)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodePipelineCustomActionType, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "codepipeline custom-action-types")
}

func scanCPWebhooks(ctx context.Context, client codePipelineAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListWebhooks(ctx, &codepipeline.ListWebhooksInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codepipeline:ListWebhooks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codepipeline:ListWebhooks: %w", err)
		}
		for _, w := range out.Webhooks {
			arn := sv(w.Arn)
			if arn == "" {
				continue
			}
			var name *string
			if w.Definition != nil {
				name = w.Definition.Name
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodePipelineWebhook, NativeID: arn,
				Name: name, Region: &region,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "codepipeline webhooks")
}
