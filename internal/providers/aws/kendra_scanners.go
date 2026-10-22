package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/kendra"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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
	registerType(restype.Descriptor{Type: TypeKendraAccessControlConfiguration, Service: "kendra", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeKendraExperience, Service: "kendra", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeKendraFeaturedResultsSet, Service: "kendra"})
	registerType(restype.Descriptor{Type: TypeKendraQuerySuggestionsBlockList, Service: "kendra"})
	registerType(restype.Descriptor{Type: TypeKendraThesaurus, Service: "kendra"})
	registerType(restype.Descriptor{Type: TypeKendraExperienceEntity, Service: "kendra"})
	registerType(restype.Descriptor{Type: TypeKendraEntityPersona, Service: "kendra"})
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
	ListExperienceEntities(context.Context, *kendra.ListExperienceEntitiesInput, ...func(*kendra.Options)) (*kendra.ListExperienceEntitiesOutput, error)
	ListEntityPersonas(context.Context, *kendra.ListEntityPersonasInput, ...func(*kendra.Options)) (*kendra.ListEntityPersonasOutput, error)
}

// scanKendra discovers Kendra indices, their per-index children, and each
// experience's entities and personas. List APIs return only IDs — synthesize
// ARNs.
func scanKendra(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kendra.NewFromConfig(acct.cfg, func(o *kendra.Options) { o.Region = region })
	return scanKendraAll(ctx, client, acct, region, st, scanID)
}

// scanKendraAll runs the older phases, which stop at their first error other
// than an access denial (a transient one included), then the experience entity
// and persona phases over the experiences stored before that error. The older
// side's transient error is demoted to a warning only beside a hard error:
// joined, it would be buried in the hard error instead of reported under its
// op.
func scanKendraAll(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, experiences, err := scanKendraOlderPhases(ctx, client, acct, region, st, scanID)
	te, ie, eerr := scanKendraExperienceEntities(ctx, client, acct, region, st, scanID, experiences)
	tp, ip, perr := scanKendraEntityPersonas(ctx, client, acct, region, st, scanID, experiences)
	return total + te + tp, inserted + ie + ip, demoteBeside(st, "kendra", acct, region, err, eerr, perr)
}

// scanKendraOlderPhases runs the phases that predate experience entities and
// personas sequentially, stopping at the first error other than an access
// denial (which warns and continues), transient errors included, and returns
// the experiences stored before it.
func scanKendraOlderPhases(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, experiences []kendraExperience, err error) {
	indexIDs, t, i, ferr := scanKendraIndices(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, nil, ferr
	}
	total += t
	inserted += i

	collectExperiences := func(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID, indexID string) (int, int, error) {
		exps, t, i, err := scanKendraExperiences(ctx, client, acct, region, st, scanID, indexID)
		experiences = append(experiences, exps...)
		return t, i, err
	}
	for _, id := range indexIDs {
		for _, phase := range []func(context.Context, kendraAPI, *account, string, *store.Store, string, string) (int, int, error){
			scanKendraDataSources,
			scanKendraFaqs,
			scanKendraAccessControlConfigurations,
			collectExperiences,
			scanKendraFeaturedResultsSets,
			scanKendraQuerySuggestionsBlockLists,
			scanKendraThesauri,
		} {
			t, i, ferr = phase(ctx, client, acct, region, st, scanID, id)
			if ferr != nil {
				return total, inserted, experiences, ferr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, experiences, nil
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

// kendraExperience is an experience stored this scan: its index, its id
// (childParent.id) and its row's NativeID (childParent.arn).
type kendraExperience struct {
	indexID string
	childParent
}

// scanKendraExperiences stores the index's experiences and returns them only
// once they are stored.
func scanKendraExperiences(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indexID string) ([]kendraExperience, int, int, error) {
	iid := indexID
	pager := kendra.NewListExperiencesPaginator(client, &kendra.ListExperiencesInput{IndexId: &iid})
	var batch []*store.Resource
	var exps []kendraExperience
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "kendra:ListExperiences", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("kendra:ListExperiences: %w", err)
		}
		for _, e := range out.SummaryItems {
			id := sv(e.Id)
			if id == "" {
				continue
			}
			nativeID := kendraChildNativeID(region, acct.ID, iid, "experience", id)
			exps = append(exps, kendraExperience{indexID: iid, childParent: childParent{id: id, arn: nativeID}})
			status := string(e.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraExperience, NativeID: nativeID,
				Name: e.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "kendra experiences")
	if err != nil {
		return nil, 0, 0, err
	}
	return exps, t, i, nil
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

const (
	kendraListExperienceEntitiesOp = "kendra:ListExperienceEntities"
	kendraListEntityPersonasOp     = "kendra:ListEntityPersonas"
)

// kendraEntityPersonaPageLimit is the model's cap on ListEntityPersonas
// MaxResults (range 1..100). ListExperienceEntities models no page size. A
// ValidationException this value provoked could not be told apart by code from
// one for an experience that is not ready, so kendraExperienceFanOut would
// warn on it rather than fail.
const kendraEntityPersonaPageLimit = 100

// kendraExperienceFanOut runs list for each experience through childFanOut,
// storing every child under its experience. Per experience:
// ResourceNotFoundException (experience or index deleted since listing) is
// skipped silently, a denial warns once for op, a transient error after the
// SDK's retries is skipped with one warning for all such experiences,
// ValidationException is skipped with one warning for all such experiences,
// and anything else fails the phase, storing none of its rows.
//
// Experiences are fanned out to in every state, and how Kendra answers for one
// still creating, failed or deleting has not been observed; if it is
// ValidationException, failing the phase would fail it on every scan, and
// skipping silently would hide a real input error, so it warns and keeps the
// siblings.
func kendraExperienceFanOut(
	ctx context.Context, st *store.Store, acct *account, region, op string, experiences []kendraExperience,
	list func(ctx context.Context, indexID string, exp childParent) ([]*store.Resource, error),
) (int, int, error) {
	parents := make([]childParent, len(experiences))
	indexOf := make(map[string]string, len(experiences))
	for i, e := range experiences {
		parents[i] = e.childParent
		indexOf[e.arn] = e.indexID
	}
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	return childFanOutSkipNotReady(ctx, st, acct, region, op, parents, isGone, byParentARN,
		"experience(s)", "the experience may not be ready (creating, failed or deleting)",
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			return list(ctx, indexOf[p.arn], p)
		})
}

// scanKendraExperienceEntities stores the IAM Identity Center users and groups
// granted access to each experience. EntityId is an Identity Center user or
// group id (by the model's pattern, an optional 10-hex-digit prefix plus a
// UUID, with no "/"), so it alone keys the row under its experience.
func scanKendraExperienceEntities(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, experiences []kendraExperience) (int, int, error) {
	return kendraExperienceFanOut(ctx, st, acct, region, kendraListExperienceEntitiesOp, experiences, func(ctx context.Context, indexID string, exp childParent) ([]*store.Resource, error) {
		pager := kendra.NewListExperienceEntitiesPaginator(client, &kendra.ListExperienceEntitiesInput{IndexId: &indexID, Id: &exp.id})
		var rows []*store.Resource
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, e := range out.SummaryItems {
				id := sv(e.EntityId)
				if id == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeKendraExperienceEntity, NativeID: exp.arn + "/entity/" + id,
					Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
}

// scanKendraEntityPersonas stores the persona (OWNER or VIEWER) each
// experience grants its users and groups. EntityId keys the row: that an
// entity holds at most one persona per experience is inferred from
// DisassociatePersonasFromEntities taking only entity ids, not observed live.
func scanKendraEntityPersonas(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, experiences []kendraExperience) (int, int, error) {
	return kendraExperienceFanOut(ctx, st, acct, region, kendraListEntityPersonasOp, experiences, func(ctx context.Context, indexID string, exp childParent) ([]*store.Resource, error) {
		pager := kendra.NewListEntityPersonasPaginator(client,
			&kendra.ListEntityPersonasInput{IndexId: &indexID, Id: &exp.id},
			func(o *kendra.ListEntityPersonasPaginatorOptions) { o.Limit = kendraEntityPersonaPageLimit })
		var rows []*store.Resource
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, p := range out.SummaryItems {
				id := sv(p.EntityId)
				if id == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeKendraEntityPersona, NativeID: exp.arn + "/persona/" + id,
					Region: &region, CreatedAt: tp(p.CreatedAt),
					AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
		return rows, nil
	})
}
