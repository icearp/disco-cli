package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/kendra"
)

// isKendraClosedToAccount detects Kendra's closed-to-new-customers state
// (discontinued 2024): NotAuthorizedException with canned "Your account is
// not authorized to make this call." body — distinct from per-action IAM denials.
func isKendraClosedToAccount(err error) bool {
	return isAPIErrorWithMessage(err, "NotAuthorizedException", "not authorized to make this call")
}

func init() {
	registerType(restype.Descriptor{Type: TypeKendraIndex, Service: "kendra"})
	registerType(restype.Descriptor{Type: TypeKendraDataSource, Service: "kendra"})
	registerType(restype.Descriptor{Type: TypeKendraFaq, Service: "kendra"})
	registerType(restype.Descriptor{Type: TypeKendraAccessControlConfiguration, Service: "kendra", Upstream: "AWS::kendra::access-control-configuration"})
	registerType(restype.Descriptor{Type: TypeKendraExperience, Service: "kendra"})
	registerType(restype.Descriptor{Type: TypeKendraFeaturedResultsSet, Service: "kendra", Upstream: "AWS::kendra::featured-results-set"})
	registerType(restype.Descriptor{Type: TypeKendraQuerySuggestionsBlockList, Service: "kendra", Upstream: "AWS::kendra::query-suggestions-block-list"})
	registerType(restype.Descriptor{Type: TypeKendraThesaurus, Service: "kendra"})
	registerService(serviceEntry{
		name: "aws:kendra",
		fn:   scanKendra,
	})
}

type kendraAPI interface {
	ListIndices(context.Context, *kendra.ListIndicesInput, ...func(*kendra.Options)) (*kendra.ListIndicesOutput, error)
	ListDataSources(context.Context, *kendra.ListDataSourcesInput, ...func(*kendra.Options)) (*kendra.ListDataSourcesOutput, error)
	ListFaqs(context.Context, *kendra.ListFaqsInput, ...func(*kendra.Options)) (*kendra.ListFaqsOutput, error)
	DescribeIndex(context.Context, *kendra.DescribeIndexInput, ...func(*kendra.Options)) (*kendra.DescribeIndexOutput, error)
	ListAccessControlConfigurations(context.Context, *kendra.ListAccessControlConfigurationsInput, ...func(*kendra.Options)) (*kendra.ListAccessControlConfigurationsOutput, error)
	ListExperiences(context.Context, *kendra.ListExperiencesInput, ...func(*kendra.Options)) (*kendra.ListExperiencesOutput, error)
	ListFeaturedResultsSets(context.Context, *kendra.ListFeaturedResultsSetsInput, ...func(*kendra.Options)) (*kendra.ListFeaturedResultsSetsOutput, error)
	ListQuerySuggestionsBlockLists(context.Context, *kendra.ListQuerySuggestionsBlockListsInput, ...func(*kendra.Options)) (*kendra.ListQuerySuggestionsBlockListsOutput, error)
	ListThesauri(context.Context, *kendra.ListThesauriInput, ...func(*kendra.Options)) (*kendra.ListThesauriOutput, error)
}

// scanKendra discovers Kendra indices, per-index data sources, and per-index
// FAQs. List APIs return only IDs — synthesize ARNs.
func scanKendra(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kendra.NewFromConfig(acct.cfg, func(o *kendra.Options) { o.Region = region })

	indexIDs, t, i, ferr := scanKendraIndices(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, id := range indexIDs {
		t, i, ferr = scanKendraDataSources(ctx, client, acct, region, st, scanID, id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanKendraFaqs(ctx, client, acct, region, st, scanID, id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		for _, phase := range []func(context.Context, kendraAPI, *account, string, *store.Store, string, string) (int, int, error){
			scanKendraAccessControlConfigurations,
			scanKendraExperiences,
			scanKendraFeaturedResultsSets,
			scanKendraQuerySuggestionsBlockLists,
			scanKendraThesauri,
		} {
			t, i, ferr = phase(ctx, client, acct, region, st, scanID, id)
			if ferr != nil {
				return total, inserted, ferr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanKendraIndices(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := kendra.NewListIndicesPaginator(client, &kendra.ListIndicesInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isKendraClosedToAccount(err) {
				return nil, 0, 0, markServiceNotEntitled(err)
			}
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "kendra:ListIndices", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("kendra:ListIndices: %w", err)
		}
		for _, idx := range out.IndexConfigurationSummaryItems {
			id := sv(idx.Id)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			arn := fmt.Sprintf("arn:aws:kendra:%s:%s:index/%s", region, acct.ID, id)
			status := string(idx.Status)
			attrsJSON := mustJSON(idx)
			if dout, derr := client.DescribeIndex(ctx, &kendra.DescribeIndexInput{Id: idx.Id}); derr == nil {
				attrsJSON = mustJSON(dout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraIndex, NativeID: arn,
				Name: idx.Name, Region: &region, Status: &status,
				AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "kendra indices")
	return ids, t, i, err
}

func scanKendraDataSources(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indexID string) (int, int, error) {
	iid := indexID
	pager := kendra.NewListDataSourcesPaginator(client, &kendra.ListDataSourcesInput{IndexId: &iid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kendra:ListDataSources", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kendra:ListDataSources: %w", err)
		}
		for _, d := range out.SummaryItems {
			id := sv(d.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:kendra:%s:%s:index/%s/data-source/%s", region, acct.ID, iid, id)
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraDataSource, NativeID: arn,
				Name: d.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kendra data-sources")
}

func scanKendraFaqs(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indexID string) (int, int, error) {
	iid := indexID
	pager := kendra.NewListFaqsPaginator(client, &kendra.ListFaqsInput{IndexId: &iid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kendra:ListFaqs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kendra:ListFaqs: %w", err)
		}
		for _, f := range out.FaqSummaryItems {
			id := sv(f.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:kendra:%s:%s:index/%s/faq/%s", region, acct.ID, iid, id)
			status := string(f.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraFaq, NativeID: arn,
				Name: f.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kendra faqs")
}

// kendraChildNativeID synthesizes the dominant `{indexARN}/<kind>/{id}` shape;
// these per-index summaries carry no AWS-issued ARN.
func kendraChildNativeID(region, acct, indexID, kind, id string) string {
	return fmt.Sprintf("arn:aws:kendra:%s:%s:index/%s/%s/%s", region, acct, indexID, kind, id)
}

func scanKendraAccessControlConfigurations(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indexID string) (int, int, error) {
	iid := indexID
	pager := kendra.NewListAccessControlConfigurationsPaginator(client, &kendra.ListAccessControlConfigurationsInput{IndexId: &iid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kendra:ListAccessControlConfigurations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kendra:ListAccessControlConfigurations: %w", err)
		}
		for _, a := range out.AccessControlConfigurations {
			id := sv(a.Id)
			if id == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraAccessControlConfiguration, NativeID: kendraChildNativeID(region, acct.ID, iid, "access-control-configuration", id),
				Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kendra access-control-configurations")
}

func scanKendraExperiences(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indexID string) (int, int, error) {
	iid := indexID
	pager := kendra.NewListExperiencesPaginator(client, &kendra.ListExperiencesInput{IndexId: &iid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kendra:ListExperiences", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kendra:ListExperiences: %w", err)
		}
		for _, e := range out.SummaryItems {
			id := sv(e.Id)
			if id == "" {
				continue
			}
			status := string(e.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraExperience, NativeID: kendraChildNativeID(region, acct.ID, iid, "experience", id),
				Name: e.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kendra experiences")
}

func scanKendraFeaturedResultsSets(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indexID string) (int, int, error) {
	iid := indexID
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListFeaturedResultsSets(ctx, &kendra.ListFeaturedResultsSetsInput{IndexId: &iid, NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kendra:ListFeaturedResultsSets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kendra:ListFeaturedResultsSets: %w", err)
		}
		for _, f := range out.FeaturedResultsSetSummaryItems {
			id := sv(f.FeaturedResultsSetId)
			if id == "" {
				continue
			}
			status := string(f.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraFeaturedResultsSet, NativeID: kendraChildNativeID(region, acct.ID, iid, "featured-results-set", id),
				Name: f.FeaturedResultsSetName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "kendra featured-results-sets")
}

func scanKendraQuerySuggestionsBlockLists(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indexID string) (int, int, error) {
	iid := indexID
	pager := kendra.NewListQuerySuggestionsBlockListsPaginator(client, &kendra.ListQuerySuggestionsBlockListsInput{IndexId: &iid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kendra:ListQuerySuggestionsBlockLists", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kendra:ListQuerySuggestionsBlockLists: %w", err)
		}
		for _, b := range out.BlockListSummaryItems {
			id := sv(b.Id)
			if id == "" {
				continue
			}
			status := string(b.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraQuerySuggestionsBlockList, NativeID: kendraChildNativeID(region, acct.ID, iid, "query-suggestions-block-list", id),
				Name: b.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kendra query-suggestions-block-lists")
}

func scanKendraThesauri(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indexID string) (int, int, error) {
	iid := indexID
	pager := kendra.NewListThesauriPaginator(client, &kendra.ListThesauriInput{IndexId: &iid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kendra:ListThesauri", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kendra:ListThesauri: %w", err)
		}
		for _, th := range out.ThesaurusSummaryItems {
			id := sv(th.Id)
			if id == "" {
				continue
			}
			status := string(th.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraThesaurus, NativeID: kendraChildNativeID(region, acct.ID, iid, "thesaurus", id),
				Name: th.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(th), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "kendra thesauri")
}
