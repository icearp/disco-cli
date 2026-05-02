package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/mediaconvert"
)

func init() {
	registerService(serviceEntry{
		name: "aws:media-convert",
		fn:   scanMediaConvert,
		emits: []coverage.TypeDecl{
			{Service: "media-convert", DiscoType: TypeMediaConvertJobTemplate},
			{Service: "media-convert", DiscoType: TypeMediaConvertPreset},
			{Service: "media-convert", DiscoType: TypeMediaConvertQueue},
		},
	})
}

type mediaConvertAPI interface {
	ListJobTemplates(context.Context, *mediaconvert.ListJobTemplatesInput, ...func(*mediaconvert.Options)) (*mediaconvert.ListJobTemplatesOutput, error)
	ListPresets(context.Context, *mediaconvert.ListPresetsInput, ...func(*mediaconvert.Options)) (*mediaconvert.ListPresetsOutput, error)
	ListQueues(context.Context, *mediaconvert.ListQueuesInput, ...func(*mediaconvert.Options)) (*mediaconvert.ListQueuesOutput, error)
}

// scanMediaConvert discovers MediaConvert job templates, presets, and queues.
// ARNs native on every type.
func scanMediaConvert(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := mediaconvert.NewFromConfig(acct.cfg, func(o *mediaconvert.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanMCJobTemplates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMCPresets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMCQueues(ctx, client, acct, region, st, scanID) },
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

func scanMCJobTemplates(ctx context.Context, client mediaConvertAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mediaconvert.NewListJobTemplatesPaginator(client, &mediaconvert.ListJobTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediaconvert:ListJobTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediaconvert:ListJobTemplates: %w", err)
		}
		for _, t := range out.JobTemplates {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConvertJobTemplate, NativeID: arn,
				Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediaconvert job-templates")
}

func scanMCPresets(ctx context.Context, client mediaConvertAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mediaconvert.NewListPresetsPaginator(client, &mediaconvert.ListPresetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediaconvert:ListPresets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediaconvert:ListPresets: %w", err)
		}
		for _, p := range out.Presets {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConvertPreset, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediaconvert presets")
}

func scanMCQueues(ctx context.Context, client mediaConvertAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := mediaconvert.NewListQueuesPaginator(client, &mediaconvert.ListQueuesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "mediaconvert:ListQueues", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("mediaconvert:ListQueues: %w", err)
		}
		for _, q := range out.Queues {
			arn := sv(q.Arn)
			if arn == "" {
				continue
			}
			status := string(q.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConvertQueue, NativeID: arn,
				Name: q.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "mediaconvert queues")
}
