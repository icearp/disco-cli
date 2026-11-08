package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	transcribetypes "github.com/aws/aws-sdk-go-v2/service/transcribe/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeTranscribeCallAnalyticsCategory, Service: "transcribe"})
	registerType(restype.Descriptor{Type: TypeTranscribeLanguageModel, Service: "transcribe"})
	registerType(restype.Descriptor{Type: TypeTranscribeVocabulary, Service: "transcribe"})
	registerType(restype.Descriptor{Type: TypeTranscribeVocabularyFilter, Service: "transcribe"})
	registerType(restype.Descriptor{Type: TypeTranscribeMedicalVocabulary, Service: "transcribe"})
	registerService(serviceEntry{
		name: "aws:transcribe",
		fn:   scanTranscribe,
	})
}

type transcribeAPI interface {
	ListCallAnalyticsCategories(context.Context, *transcribe.ListCallAnalyticsCategoriesInput, ...func(*transcribe.Options)) (*transcribe.ListCallAnalyticsCategoriesOutput, error)
	ListLanguageModels(context.Context, *transcribe.ListLanguageModelsInput, ...func(*transcribe.Options)) (*transcribe.ListLanguageModelsOutput, error)
	ListVocabularies(context.Context, *transcribe.ListVocabulariesInput, ...func(*transcribe.Options)) (*transcribe.ListVocabulariesOutput, error)
	ListVocabularyFilters(context.Context, *transcribe.ListVocabularyFiltersInput, ...func(*transcribe.Options)) (*transcribe.ListVocabularyFiltersOutput, error)
	ListMedicalVocabularies(context.Context, *transcribe.ListMedicalVocabulariesInput, ...func(*transcribe.Options)) (*transcribe.ListMedicalVocabulariesOutput, error)
	ListTagsForResource(context.Context, *transcribe.ListTagsForResourceInput, ...func(*transcribe.Options)) (*transcribe.ListTagsForResourceOutput, error)
}

const (
	transcribeListCategoriesOp          = "transcribe:ListCallAnalyticsCategories"
	transcribeListLanguageModelsOp      = "transcribe:ListLanguageModels"
	transcribeListVocabulariesOp        = "transcribe:ListVocabularies"
	transcribeListVocabularyFiltersOp   = "transcribe:ListVocabularyFilters"
	transcribeListMedicalVocabulariesOp = "transcribe:ListMedicalVocabularies"
	transcribeTagsOp                    = "transcribe:ListTagsForResource"
	// transcribePageCap is the maximum of every listing's MaxResults range
	// (1-100); a listing sent none gets pages of 5.
	transcribePageCap = 100
)

// transcribeNativeID builds the ARN of a name-addressed Transcribe resource
// in the Service Reference's format, kind being its resource-type segment:
// no listed element carries an ARN.
func transcribeNativeID(partition, region, acct, kind, name string) string {
	return arnFor(partition, "transcribe", region, acct, kind+"/"+name)
}

// isTranscribeFeatureGap reports whether err is Transcribe's answer for a
// sub-feature (Call Analytics) missing in a region where Transcribe
// otherwise works: a per-op gap, so only that listing is empty. Not an IAM
// denial (those are AccessDenied, warned separately).
func isTranscribeFeatureGap(err error) bool {
	return isAPIErrorWithMessage(err, "BadRequestException", "isn't supported in this region")
}

// isTranscribeNotOffered reports whether err is Transcribe's answer when the
// service itself is not offered to the account in this region. The SSM
// global-infra catalog can list Transcribe in a region where every op is
// rejected (eu-north-1 when this was written), so the region-scoping
// preflight can't exclude it — the scanner is the backstop and reports the region
// unavailable (transcribeGate). Not an IAM denial.
func isTranscribeNotOffered(err error) bool {
	return isAPIErrorWithMessage(err, "BadRequestException", "isn't authorized to call this operation")
}

// transcribeGate records, for one scan, the isTranscribeNotOffered answer of
// a listing's first page while answered is false: no page read before it
// listed an item or failed other than by a denial. Nothing was stored or
// failed by then, so the scan reports the region unavailable and runs no
// later listing. A denied listing does not count as an answer, as before
// tags were read: its warning stays, and a later listing can still show the
// service absent.
type transcribeGate struct {
	notOffered error
	answered   bool
}

// transcribePages reads a listing's pages with read. A first page answering
// isTranscribeFeatureGap is an empty listing; one answering
// isTranscribeNotOffered before gate was answered is recorded in gate and
// ends the listing empty. Either answer on a later page, and a not-offered
// answer once gate was answered, is returned as the error it is coded as: it
// follows answers that showed the service served here.
func transcribePages[Item any](gate *transcribeGate, read func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	read = firstPageGapPages(isTranscribeFeatureGap, read)
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := read(ctx, token)
		if token == nil && !gate.answered && isTranscribeNotOffered(err) {
			gate.notOffered = err
			return nil, nil, nil
		}
		if len(items) > 0 || (err != nil && !isAccessDenied(err)) {
			gate.answered = true
		}
		return items, next, err
	}
}

// transcribeTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN transcribeNativeID builds. The op's documentation names
// vocabulary, medical-vocabulary, vocabulary-filter and language-model among
// the resource types it accepts; the Service Reference gives each
// aws:ResourceTag, and each Create input takes Tags. A call analytics
// category is not read: its listed element carries its tags.
// NotFoundException, the op's modeled not-found, drops a resource deleted
// after it was listed.
func transcribeTags(client transcribeAPI) rowTagReader {
	return rowTagReader{
		op:       transcribeTagsOp,
		notFound: []string{"NotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &transcribe.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return transcribeTagsJSON(out.Tags), nil
		},
	}
}

func transcribeTagsJSON(tags []transcribetypes.Tag) *string {
	return kvTagsJSON(tags, func(t transcribetypes.Tag) (*string, *string) { return t.Key, t.Value })
}

// scanTranscribe stores the region's call analytics categories, language
// models, vocabularies, vocabulary filters and medical vocabularies, with
// their tags. Every listing runs as its own phase, one at a time: a phase's
// error does not stop the later ones; a failed write and ctx's end do
// (runHostPhases), and so does a region that does not offer Transcribe
// (transcribeGate). The tag reads' denials are one warning for the scan.
func scanTranscribe(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := transcribe.NewFromConfig(acct.cfg, func(o *transcribe.Options) { o.Region = region })
	var gate transcribeGate
	tags := transcribeTags(client).sharingDenials()
	phase := func(op string, run func(p *phaseTotals) bool) hostPhase {
		return hostPhase{op: op, run: func(p *phaseTotals) bool {
			if gate.notOffered != nil {
				return false
			}
			return run(p)
		}}
	}
	total, err := runHostPhases(ctx, st, acct, region, "transcribe", 0, []hostPhase{
		phase(transcribeListCategoriesOp, func(p *phaseTotals) bool {
			return scanTranscribeCallAnalyticsCategories(ctx, client, acct, region, st, scanID, &gate, p)
		}),
		phase(transcribeListLanguageModelsOp, func(p *phaseTotals) bool {
			return scanTranscribeLanguageModels(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase(transcribeListVocabulariesOp, func(p *phaseTotals) bool {
			return scanTranscribeVocabularies(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase(transcribeListVocabularyFiltersOp, func(p *phaseTotals) bool {
			return scanTranscribeVocabularyFilters(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase(transcribeListMedicalVocabulariesOp, func(p *phaseTotals) bool {
			return scanTranscribeMedicalVocabularies(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
	})
	tags.warnDenials(st, acct, region)
	if gate.notOffered != nil {
		return total, markServiceUnavailable(gate.notOffered)
	}
	return total, err
}

// transcribeRow is the row of a listed element of typ, named name and
// stored under the ARN transcribeNativeID builds from kind and name. An
// element without a name is not stored: the name is its only identifier.
// what names the listing's elements in that warning.
func transcribeRow(acct *account, region, scanID, typ, kind, what string, name, status *string, element any) (*store.Resource, string, string) {
	n := sv(name)
	if n == "" {
		return nil, what + " have no name", noIdentifier
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: transcribeNativeID(acct.partition(), region, acct.ID, kind, n),
		Name: &n, Region: &region, Status: status,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}, "", ""
}

// scanTranscribeCallAnalyticsCategories stores the call analytics
// categories, each with the tags its listed element carries. It reports
// whether the write failed, after which the scan must stop.
func scanTranscribeCallAnalyticsCategories(ctx context.Context, client transcribeAPI, acct *account, region string, st *store.Store, scanID string, gate *transcribeGate, p *phaseTotals) bool {
	total, err := storeTokenList(ctx, st, acct, region, transcribeListCategoriesOp,
		transcribePages(gate, func(ctx context.Context, token *string) ([]transcribetypes.CategoryProperties, *string, error) {
			out, err := client.ListCallAnalyticsCategories(ctx, &transcribe.ListCallAnalyticsCategoriesInput{MaxResults: sdkaws.Int32(transcribePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Categories, out.NextToken, nil
		}),
		func(c transcribetypes.CategoryProperties) (*store.Resource, string, string) {
			r, why, key := transcribeRow(acct, region, scanID, TypeTranscribeCallAnalyticsCategory, "analytics-category", "call analytics categories", c.CategoryName, nil, c)
			if r != nil {
				r.TagsJSON = transcribeTagsJSON(c.Tags)
			}
			return r, why, key
		})
	return p.addPhase(transcribeListCategoriesOp, total, err)
}

// scanTranscribeLanguageModels stores the custom language models, each page
// written as soon as its rows' tags are read. It reports whether a write
// failed, after which the scan must stop.
func scanTranscribeLanguageModels(ctx context.Context, client transcribeAPI, acct *account, region string, st *store.Store, scanID string, gate *transcribeGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, transcribeListLanguageModelsOp,
		transcribePages(gate, func(ctx context.Context, token *string) ([]transcribetypes.LanguageModel, *string, error) {
			out, err := client.ListLanguageModels(ctx, &transcribe.ListLanguageModelsInput{MaxResults: sdkaws.Int32(transcribePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Models, out.NextToken, nil
		}),
		func(m transcribetypes.LanguageModel) (*store.Resource, string, string) {
			return transcribeRow(acct, region, scanID, TypeTranscribeLanguageModel, "language-model", "language models", m.ModelName, sdkaws.String(string(m.ModelStatus)), m)
		}, tags, p)
}

// scanTranscribeVocabularies stores the custom vocabularies, each page
// written as soon as its rows' tags are read. It reports whether a write
// failed, after which the scan must stop.
func scanTranscribeVocabularies(ctx context.Context, client transcribeAPI, acct *account, region string, st *store.Store, scanID string, gate *transcribeGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, transcribeListVocabulariesOp,
		transcribePages(gate, func(ctx context.Context, token *string) ([]transcribetypes.VocabularyInfo, *string, error) {
			out, err := client.ListVocabularies(ctx, &transcribe.ListVocabulariesInput{MaxResults: sdkaws.Int32(transcribePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Vocabularies, out.NextToken, nil
		}),
		func(v transcribetypes.VocabularyInfo) (*store.Resource, string, string) {
			return transcribeRow(acct, region, scanID, TypeTranscribeVocabulary, "vocabulary", "vocabularies", v.VocabularyName, sdkaws.String(string(v.VocabularyState)), v)
		}, tags, p)
}

// scanTranscribeVocabularyFilters stores the custom vocabulary filters, each
// page written as soon as its rows' tags are read. It reports whether a
// write failed, after which the scan must stop.
func scanTranscribeVocabularyFilters(ctx context.Context, client transcribeAPI, acct *account, region string, st *store.Store, scanID string, gate *transcribeGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, transcribeListVocabularyFiltersOp,
		transcribePages(gate, func(ctx context.Context, token *string) ([]transcribetypes.VocabularyFilterInfo, *string, error) {
			out, err := client.ListVocabularyFilters(ctx, &transcribe.ListVocabularyFiltersInput{MaxResults: sdkaws.Int32(transcribePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VocabularyFilters, out.NextToken, nil
		}),
		func(f transcribetypes.VocabularyFilterInfo) (*store.Resource, string, string) {
			return transcribeRow(acct, region, scanID, TypeTranscribeVocabularyFilter, "vocabulary-filter", "vocabulary filters", f.VocabularyFilterName, nil, f)
		}, tags, p)
}

// scanTranscribeMedicalVocabularies stores the custom medical vocabularies,
// each page written as soon as its rows' tags are read. Their ARNs have
// their own resource type, medical-vocabulary. It reports whether a write
// failed, after which the scan must stop.
func scanTranscribeMedicalVocabularies(ctx context.Context, client transcribeAPI, acct *account, region string, st *store.Store, scanID string, gate *transcribeGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, transcribeListMedicalVocabulariesOp,
		transcribePages(gate, func(ctx context.Context, token *string) ([]transcribetypes.VocabularyInfo, *string, error) {
			out, err := client.ListMedicalVocabularies(ctx, &transcribe.ListMedicalVocabulariesInput{MaxResults: sdkaws.Int32(transcribePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Vocabularies, out.NextToken, nil
		}),
		func(v transcribetypes.VocabularyInfo) (*store.Resource, string, string) {
			return transcribeRow(acct, region, scanID, TypeTranscribeMedicalVocabulary, "medical-vocabulary", "medical vocabularies", v.VocabularyName, sdkaws.String(string(v.VocabularyState)), v)
		}, tags, p)
}
