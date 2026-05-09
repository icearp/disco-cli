package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/kendra"
)

// isKendraClosedToAccount detects the closed-to-new-customers state for
// Amazon Kendra (discontinued for new customers in 2024). Surfaces as
// NotAuthorizedException with the canned "Your account is not authorized
// to make this call." body — distinct from per-action IAM denials.
func isKendraClosedToAccount(err error) bool {
	return isAPIErrorWithMessage(err, "NotAuthorizedException", "not authorized to make this call")
}

func init() {
	registerService(serviceEntry{
		name: "aws:kendra",
		fn:   scanKendra,
		emits: []coverage.TypeDecl{
			{Service: "kendra", DiscoType: TypeKendraIndex},
			{Service: "kendra", DiscoType: TypeKendraDataSource},
			{Service: "kendra", DiscoType: TypeKendraFaq},
		},
	})
}

type kendraAPI interface {
	ListIndices(context.Context, *kendra.ListIndicesInput, ...func(*kendra.Options)) (*kendra.ListIndicesOutput, error)
	ListDataSources(context.Context, *kendra.ListDataSourcesInput, ...func(*kendra.Options)) (*kendra.ListDataSourcesOutput, error)
	ListFaqs(context.Context, *kendra.ListFaqsInput, ...func(*kendra.Options)) (*kendra.ListFaqsOutput, error)
	DescribeIndex(context.Context, *kendra.DescribeIndexInput, ...func(*kendra.Options)) (*kendra.DescribeIndexOutput, error)
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
				return nil, 0, 0, markServiceDisabled(err)
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
