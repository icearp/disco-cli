package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mediaconvert"
	mctypes "github.com/aws/aws-sdk-go-v2/service/mediaconvert/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// A job template's HLS or CMAF AES encryption can carry a static content
	// key (StaticKeyProvider.StaticKeyValue, 32 hex characters) that decrypts
	// every output the template protects. Presets carry no key provider.
	registerType(restype.Descriptor{Type: TypeMediaConvertJobTemplate, Service: "media-convert", Redact: []redact.Rule{
		{Path: "Settings.OutputGroups[*].OutputGroupSettings.HlsGroupSettings.Encryption.StaticKeyProvider.StaticKeyValue", Mode: redact.RedactScalar},
		{Path: "Settings.OutputGroups[*].OutputGroupSettings.CmafGroupSettings.Encryption.StaticKeyProvider.StaticKeyValue", Mode: redact.RedactScalar},
	}})
	registerType(restype.Descriptor{Type: TypeMediaConvertPreset, Service: "media-convert"})
	registerType(restype.Descriptor{Type: TypeMediaConvertQueue, Service: "media-convert"})
	registerService(serviceEntry{
		name: "aws:media-convert",
		fn:   scanMediaConvert,
	})
}

const (
	mcvListJobTemplatesOp    = "mediaconvert:ListJobTemplates"
	mcvListPresetsOp         = "mediaconvert:ListPresets"
	mcvListQueuesOp          = "mediaconvert:ListQueues"
	mcvListTagsForResourceOp = "mediaconvert:ListTagsForResource"
)

// mcvPageSize is the MaxResults every listing sends: the model's maximum
// (range 1-20) for all three.
const mcvPageSize int32 = 20

type mediaConvertAPI interface {
	ListJobTemplates(context.Context, *mediaconvert.ListJobTemplatesInput, ...func(*mediaconvert.Options)) (*mediaconvert.ListJobTemplatesOutput, error)
	ListPresets(context.Context, *mediaconvert.ListPresetsInput, ...func(*mediaconvert.Options)) (*mediaconvert.ListPresetsOutput, error)
	ListQueues(context.Context, *mediaconvert.ListQueuesInput, ...func(*mediaconvert.Options)) (*mediaconvert.ListQueuesOutput, error)
	ListTagsForResource(context.Context, *mediaconvert.ListTagsForResourceInput, ...func(*mediaconvert.Options)) (*mediaconvert.ListTagsForResourceOutput, error)
}

// scanMediaConvert discovers MediaConvert job templates, presets and queues,
// each stored under the ARN its listing returns, one listing at a time
// (runHostPhases): a listing's error does not stop the later ones; a failed
// write and ctx's end do. Each listing is written a page at a time with its
// rows' tags read (mcvTags), and the tag denials of the whole scan are one
// warning.
func scanMediaConvert(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := mediaconvert.NewFromConfig(acct.cfg, func(o *mediaconvert.Options) { o.Region = region })
	tags := mcvTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "mediaconvert", 0, []hostPhase{
		{op: mcvListJobTemplatesOp, run: func(p *phaseTotals) bool {
			return scanMCJobTemplates(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: mcvListPresetsOp, run: func(p *phaseTotals) bool {
			return scanMCPresets(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: mcvListQueuesOp, run: func(p *phaseTotals) bool {
			return scanMCQueues(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// mcvTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference lists
// JobTemplate, Preset and Queue under ListTagsForResource and TagResource,
// and each Create op takes Tags. The op does not page its tags.
// NotFoundException, the op's modeled not-found, drops a resource deleted
// after it was listed. The listings return this account's own resources.
//
// A ManagedByProvider row (Type SYSTEM: a built-in job template, preset or
// queue, which the model says the user can neither modify nor delete) is not
// read: whether the op accepts its ARN is unverified, a refusal would fail
// every scan (every region lists them), and a not-found answer would drop
// the row. Such a row is stored untagged.
func mcvTags(client mediaConvertAPI) rowTagReader {
	return rowTagReader{
		op:       mcvListTagsForResourceOp,
		notFound: []string{"NotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return nil, nil
			}
			out, err := client.ListTagsForResource(ctx, &mediaconvert.ListTagsForResourceInput{Arn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			if out.ResourceTags == nil {
				// The model marks ResourceTags optional; a response
				// without it names no tag.
				return nil, nil
			}
			return mapTagsJSON(out.ResourceTags.Tags), nil
		},
	}
}

// scanMCJobTemplates stores the job templates, the built-in (SYSTEM) ones
// ManagedByProvider. It reports whether a write failed, after which the scan
// must stop.
func scanMCJobTemplates(ctx context.Context, client mediaConvertAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, mcvListJobTemplatesOp,
		func(ctx context.Context, token *string) ([]mctypes.JobTemplate, *string, error) {
			out, err := client.ListJobTemplates(ctx, &mediaconvert.ListJobTemplatesInput{MaxResults: sdkaws.Int32(mcvPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.JobTemplates, out.NextToken, nil
		},
		func(t mctypes.JobTemplate) (*store.Resource, string, string) {
			return arnRow("job templates", t.Arn, t.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConvertJobTemplate, NativeID: sv(t.Arn),
				Name: t.Name, Region: &region, CreatedAt: tp(t.CreatedAt),
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
				ManagedByProvider: t.Type == mctypes.TypeSystem,
			})
		},
		tags, p)
}

// scanMCPresets stores the presets, the built-in (SYSTEM) ones
// ManagedByProvider. It reports whether a write failed, after which the scan
// must stop.
func scanMCPresets(ctx context.Context, client mediaConvertAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, mcvListPresetsOp,
		func(ctx context.Context, token *string) ([]mctypes.Preset, *string, error) {
			out, err := client.ListPresets(ctx, &mediaconvert.ListPresetsInput{MaxResults: sdkaws.Int32(mcvPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Presets, out.NextToken, nil
		},
		func(pr mctypes.Preset) (*store.Resource, string, string) {
			return arnRow("presets", pr.Arn, pr.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConvertPreset, NativeID: sv(pr.Arn),
				Name: pr.Name, Region: &region, CreatedAt: tp(pr.CreatedAt),
				AttributesJSON: mustJSON(pr), DiscoveredBy: scanID,
				ManagedByProvider: pr.Type == mctypes.TypeSystem,
			})
		},
		tags, p)
}

// scanMCQueues stores the queues, the built-in (SYSTEM) default queue every
// account has ManagedByProvider. It reports whether a write failed, after
// which the scan must stop.
func scanMCQueues(ctx context.Context, client mediaConvertAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, mcvListQueuesOp,
		func(ctx context.Context, token *string) ([]mctypes.Queue, *string, error) {
			out, err := client.ListQueues(ctx, &mediaconvert.ListQueuesInput{MaxResults: sdkaws.Int32(mcvPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Queues, out.NextToken, nil
		},
		func(q mctypes.Queue) (*store.Resource, string, string) {
			return arnRow("queues", q.Arn, q.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMediaConvertQueue, NativeID: sv(q.Arn),
				Name: q.Name, Region: &region, Status: nonEmptyPtr(string(q.Status)), CreatedAt: tp(q.CreatedAt),
				AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
				ManagedByProvider: q.Type == mctypes.TypeSystem,
			})
		},
		tags, p)
}
