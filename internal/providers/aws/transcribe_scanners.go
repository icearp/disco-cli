package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
)

func init() {
	registerService(serviceEntry{
		name: "aws:transcribe",
		fn:   scanTranscribe,
		emits: []coverage.TypeDecl{
			{Service: "transcribe", DiscoType: TypeTranscribeCallAnalyticsCategory, Leaf: true},
			{Service: "transcribe", DiscoType: TypeTranscribeLanguageModel, Leaf: true},
			{Service: "transcribe", DiscoType: TypeTranscribeVocabulary, Leaf: true},
			{Service: "transcribe", DiscoType: TypeTranscribeVocabularyFilter, Leaf: true},
			{Service: "transcribe", DiscoType: TypeTranscribeMedicalVocabulary, Leaf: true},
		},
	})
}

type transcribeAPI interface {
	ListCallAnalyticsCategories(context.Context, *transcribe.ListCallAnalyticsCategoriesInput, ...func(*transcribe.Options)) (*transcribe.ListCallAnalyticsCategoriesOutput, error)
	ListLanguageModels(context.Context, *transcribe.ListLanguageModelsInput, ...func(*transcribe.Options)) (*transcribe.ListLanguageModelsOutput, error)
	ListVocabularies(context.Context, *transcribe.ListVocabulariesInput, ...func(*transcribe.Options)) (*transcribe.ListVocabulariesOutput, error)
	ListVocabularyFilters(context.Context, *transcribe.ListVocabularyFiltersInput, ...func(*transcribe.Options)) (*transcribe.ListVocabularyFiltersOutput, error)
	ListMedicalVocabularies(context.Context, *transcribe.ListMedicalVocabulariesInput, ...func(*transcribe.Options)) (*transcribe.ListMedicalVocabulariesOutput, error)
}

// transcribeNativeID synthesizes an ARN for name-addressed Transcribe
// resources (SDK returns names, not ARNs).
func transcribeNativeID(region, acct, kind, name string) string {
	return fmt.Sprintf("arn:aws:transcribe:%s:%s:%s/%s", region, acct, kind, name)
}

// transcribeRegionErr classifies Transcribe's two BadRequestException
// availability rejections, which differ in scope:
//
//   - "isn't supported in this region": a sub-feature (Call Analytics) missing
//     in a region where Transcribe otherwise works — a per-op gap, so skip
//     this phase only (returns nil, siblings continue).
//   - "isn't authorized to call this operation": Transcribe itself isn't
//     offered to the account in this region. The SSM global-infra catalog
//     over-reports Transcribe here (lists eu-north-1 among 24 regions, but
//     every op is rejected), so the region-scoping preflight can't exclude it
//     — the scanner is the backstop. Whole service absent, so mark
//     unavailable: halts remaining phases with a single (region: unavailable)
//     marker instead of a per-op skip cascade.
//
// Neither is an IAM denial (those are AccessDenied, warned separately).
// Returns (handled, out): handled=false leaves err for the caller.
func transcribeRegionErr(err error) (handled bool, out error) {
	switch {
	case isAPIErrorWithMessage(err, "BadRequestException", "isn't supported in this region"):
		return true, nil
	case isAPIErrorWithMessage(err, "BadRequestException", "isn't authorized to call this operation"):
		return true, markServiceUnavailable(err)
	}
	return false, nil
}

func scanTranscribe(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := transcribe.NewFromConfig(acct.cfg, func(o *transcribe.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanTranscribeCallAnalyticsCategories(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanTranscribeLanguageModels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanTranscribeVocabularies(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanTranscribeVocabularyFilters(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanTranscribeMedicalVocabularies(ctx, client, acct, region, st, scanID)
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

func scanTranscribeCallAnalyticsCategories(ctx context.Context, client transcribeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := transcribe.NewListCallAnalyticsCategoriesPaginator(client, &transcribe.ListCallAnalyticsCategoriesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "transcribe:ListCallAnalyticsCategories", acct.ID, region, perr)
			}
			if handled, out := transcribeRegionErr(perr); handled {
				return 0, 0, out
			}
			return 0, 0, fmt.Errorf("transcribe:ListCallAnalyticsCategories: %w", perr)
		}
		for _, c := range out.Categories {
			name := sv(c.CategoryName)
			if name == "" {
				continue
			}
			nm := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTranscribeCallAnalyticsCategory, NativeID: transcribeNativeID(region, acct.ID, "call-analytics-category", name),
				Name: &nm, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transcribe call-analytics-categories")
}

func scanTranscribeLanguageModels(ctx context.Context, client transcribeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := transcribe.NewListLanguageModelsPaginator(client, &transcribe.ListLanguageModelsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "transcribe:ListLanguageModels", acct.ID, region, perr)
			}
			if handled, out := transcribeRegionErr(perr); handled {
				return 0, 0, out
			}
			return 0, 0, fmt.Errorf("transcribe:ListLanguageModels: %w", perr)
		}
		for _, m := range out.Models {
			name := sv(m.ModelName)
			if name == "" {
				continue
			}
			nm := name
			status := string(m.ModelStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTranscribeLanguageModel, NativeID: transcribeNativeID(region, acct.ID, "language-model", name),
				Name: &nm, Region: &region, Status: &status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transcribe language-models")
}

func scanTranscribeVocabularies(ctx context.Context, client transcribeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := transcribe.NewListVocabulariesPaginator(client, &transcribe.ListVocabulariesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "transcribe:ListVocabularies", acct.ID, region, perr)
			}
			if handled, out := transcribeRegionErr(perr); handled {
				return 0, 0, out
			}
			return 0, 0, fmt.Errorf("transcribe:ListVocabularies: %w", perr)
		}
		for _, v := range out.Vocabularies {
			name := sv(v.VocabularyName)
			if name == "" {
				continue
			}
			nm := name
			status := string(v.VocabularyState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTranscribeVocabulary, NativeID: transcribeNativeID(region, acct.ID, "vocabulary", name),
				Name: &nm, Region: &region, Status: &status,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transcribe vocabularies")
}

func scanTranscribeVocabularyFilters(ctx context.Context, client transcribeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := transcribe.NewListVocabularyFiltersPaginator(client, &transcribe.ListVocabularyFiltersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "transcribe:ListVocabularyFilters", acct.ID, region, perr)
			}
			if handled, out := transcribeRegionErr(perr); handled {
				return 0, 0, out
			}
			return 0, 0, fmt.Errorf("transcribe:ListVocabularyFilters: %w", perr)
		}
		for _, f := range out.VocabularyFilters {
			name := sv(f.VocabularyFilterName)
			if name == "" {
				continue
			}
			nm := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTranscribeVocabularyFilter, NativeID: transcribeNativeID(region, acct.ID, "vocabulary-filter", name),
				Name: &nm, Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transcribe vocabulary-filters")
}

func scanTranscribeMedicalVocabularies(ctx context.Context, client transcribeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := transcribe.NewListMedicalVocabulariesPaginator(client, &transcribe.ListMedicalVocabulariesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "transcribe:ListMedicalVocabularies", acct.ID, region, perr)
			}
			if handled, out := transcribeRegionErr(perr); handled {
				return 0, 0, out
			}
			return 0, 0, fmt.Errorf("transcribe:ListMedicalVocabularies: %w", perr)
		}
		for _, v := range out.Vocabularies {
			name := sv(v.VocabularyName)
			if name == "" {
				continue
			}
			nm := name
			status := string(v.VocabularyState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTranscribeMedicalVocabulary, NativeID: transcribeNativeID(region, acct.ID, "medical-vocabulary", name),
				Name: &nm, Region: &region, Status: &status,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "transcribe medical-vocabularies")
}
