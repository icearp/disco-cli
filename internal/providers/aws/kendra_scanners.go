package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kendra"
	kendratypes "github.com/aws/aws-sdk-go-v2/service/kendra/types"
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
	ListTagsForResource(context.Context, *kendra.ListTagsForResourceInput, ...func(*kendra.Options)) (*kendra.ListTagsForResourceOutput, error)
}

const (
	// kendraPageCap is the model's MaxResults cap (1 to 100) of the index
	// listing and of every per-index listing.
	kendraPageCap = 100

	kendraTagsOp                     = "kendra:ListTagsForResource"
	kendraListIndicesOp              = "kendra:ListIndices"
	kendraDescribeIndexOp            = "kendra:DescribeIndex"
	kendraListDataSourcesOp          = "kendra:ListDataSources"
	kendraListFaqsOp                 = "kendra:ListFaqs"
	kendraListAccessControlConfigsOp = "kendra:ListAccessControlConfigurations"
	kendraListExperiencesOp          = "kendra:ListExperiences"
	kendraListFeaturedResultsSetsOp  = "kendra:ListFeaturedResultsSets"
	kendraListBlockListsOp           = "kendra:ListQuerySuggestionsBlockLists"
	kendraListThesauriOp             = "kendra:ListThesauri"
)

// kendraNotFound is the not-found code DescribeIndex and every per-index
// listing model: an index deleted since it was listed.
var kendraNotFound = []string{"ResourceNotFoundException"}

// scanKendra discovers Kendra indices, their per-index children, and each
// experience's entities and personas. List APIs return only IDs — synthesize
// ARNs.
func scanKendra(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kendra.NewFromConfig(acct.cfg, func(o *kendra.Options) { o.Region = region })
	return scanKendraAll(ctx, client, acct, region, st, scanID)
}

// scanKendraAll runs every Kendra listing as its own phase, one at a time:
// the indices, then each kind of per-index child over the indices stored,
// then the entities and personas of the experiences stored. A phase's error
// does not stop the later ones; a failed write and ctx's end do; a region
// whose index listing finds no endpoint runs no other phase
// (runHostPhases). Indices and the five taggable per-index kinds have their
// tags read per row (kendraTags), and the tag denials of the whole scan are
// one warning. An account Kendra is closed to has no index listed and is
// reported not entitled.
func scanKendraAll(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := kendraTags(client).sharingDenials()
	var (
		indices     []childParent
		closed      error
		experiences []kendraExperience
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "kendra", 0, []hostPhase{
		{op: kendraListIndicesOp, run: func(p *phaseTotals) (failed bool) {
			indices, failed, closed = scanKendraIndices(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: kendraListDataSourcesOp, run: func(p *phaseTotals) bool {
			return scanKendraDataSources(ctx, client, acct, region, st, scanID, indices, tags, p)
		}},
		{op: kendraListFaqsOp, run: func(p *phaseTotals) bool {
			return scanKendraFaqs(ctx, client, acct, region, st, scanID, indices, tags, p)
		}},
		countedPhase(0, kendraListAccessControlConfigsOp, func() (int, int, error) {
			return scanKendraAccessControlConfigurations(ctx, client, acct, region, st, scanID, indices)
		}),
		countedPhase(0, kendraListExperiencesOp, func() (t, i int, err error) {
			experiences, t, i, err = scanKendraExperiences(ctx, client, acct, region, st, scanID, indices)
			return t, i, err
		}),
		{op: kendraListFeaturedResultsSetsOp, run: func(p *phaseTotals) bool {
			return scanKendraFeaturedResultsSets(ctx, client, acct, region, st, scanID, indices, tags, p)
		}},
		{op: kendraListBlockListsOp, run: func(p *phaseTotals) bool {
			return scanKendraQuerySuggestionsBlockLists(ctx, client, acct, region, st, scanID, indices, tags, p)
		}},
		{op: kendraListThesauriOp, run: func(p *phaseTotals) bool {
			return scanKendraThesauri(ctx, client, acct, region, st, scanID, indices, tags, p)
		}},
		countedPhase(0, kendraListExperienceEntitiesOp, func() (int, int, error) {
			return scanKendraExperienceEntities(ctx, client, acct, region, st, scanID, experiences)
		}),
		countedPhase(0, kendraListEntityPersonasOp, func() (int, int, error) {
			return scanKendraEntityPersonas(ctx, client, acct, region, st, scanID, experiences)
		}),
	})
	// Closed, the index listing stored nothing, so no later phase had an
	// index or experience to read.
	if closed != nil {
		return 0, 0, markServiceNotEntitled(closed)
	}
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// kendraTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN the Service Reference gives its type
// (index/{IndexId}, or index/{IndexId}/{kind}/{Id} for an index's
// children), built by the scan because no Kendra listing returns one. The op
// answers with every tag at once (no page token). It models no not-found
// code, so no tag-read error drops a row. Its one resource-level error,
// ResourceUnavailableException ("isn't available"), is not taken as one:
// Kendra's model lists it beside ResourceNotFoundException on SubmitFeedback,
// so the two mean different things. That error keeps the row without tags and
// is returned, as any other error is. Known race: a resource deleted after it
// was listed is kept untagged and its tag read's error makes the scan
// partial. Every listing read here returns the account's own resources.
func kendraTags(client kendraAPI) rowTagReader {
	return rowTagReader{
		op: kendraTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &kendra.ListTagsForResourceInput{ResourceARN: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t kendratypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanKendraIndices stores the region's indices a page at a time with their
// tags, each row the index's DescribeIndex body. A DescribeIndex
// ResourceNotFoundException drops an index deleted since it was listed; any
// other failure keeps the list element as the row (rowDetailReader). It
// returns the indices stored, as the parents of the per-index listings,
// whether a write failed, and the error of a first page refused because
// Kendra is closed to the account, which then stores nothing.
func scanKendraIndices(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (indices []childParent, storeWriteFailed bool, closed error) {
	// ids maps each row of the page in flight to its index id, which
	// DescribeIndex and the per-index listings take. The row builder fills it
	// and the reads only look entries up; it is emptied before the next page
	// is fetched, which follows that page's write, so one page is held.
	ids := map[*store.Resource]string{}
	steps := tokenPageSteps{
		details: rowDetailReader{
			op: kendraDescribeIndexOp, what: "index(es)", notFound: kendraNotFound,
			get: func(ctx context.Context, r *store.Resource) (any, error) {
				out, err := client.DescribeIndex(ctx, &kendra.DescribeIndexInput{Id: sdkaws.String(ids[r])})
				if err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		stored: func(rows []*store.Resource) {
			for _, r := range rows {
				indices = append(indices, childParent{id: ids[r], arn: r.NativeID})
			}
		},
	}
	isClosed := func(err error) bool {
		if isKendraClosedToAccount(err) {
			closed = err
			return true
		}
		return false
	}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, kendraListIndicesOp,
		firstPageGapPages(isClosed, func(ctx context.Context, token *string) ([]kendratypes.IndexConfigurationSummary, *string, error) {
			clear(ids)
			out, err := client.ListIndices(ctx, &kendra.ListIndicesInput{NextToken: token, MaxResults: sdkaws.Int32(kendraPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.IndexConfigurationSummaryItems, out.NextToken, nil
		}),
		func(idx kendratypes.IndexConfigurationSummary) (*store.Resource, string, string) {
			id := sv(idx.Id)
			if id == "" {
				return nil, "indices have an empty Id", cmp.Or(sv(idx.Name), "(an index with no name)")
			}
			status := string(idx.Status)
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKendraIndex, NativeID: arnFor(acct.partition(), "kendra", region, acct.ID, "index/"+id),
				Name: idx.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(idx), DiscoveredBy: scanID,
			}
			ids[r] = id
			return r, "", ""
		}, steps, tags, p)
	return indices, failed, closed
}

// kendraChildNativeID synthesizes the dominant `{indexARN}/<kind>/{id}` shape;
// these per-index summaries carry no AWS-issued ARN.
func kendraChildNativeID(partition, region, acct, indexID, kind, id string) string {
	return arnFor(partition, "kendra", region, acct, "index/"+indexID+"/"+kind+"/"+id)
}

// kendraChildRow is the row of an item of kind listed for index indexID,
// keyed by kendraChildNativeID, the item verbatim as the attributes. An item
// without an id is not stored, as one empty id would merge every such item
// of the index into one resource; what names the items in that warning.
func kendraChildRow(acct *account, region, scanID, typ, what, kind, indexID string, id, name, status *string, item any) (*store.Resource, string, string) {
	if sv(id) == "" {
		return nil, what + " have an empty Id", cmp.Or(sv(name), "(an item with no name)") + " of index " + indexID
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: kendraChildNativeID(acct.partition(), region, acct.ID, indexID, kind, sv(id)),
		Name: name, Region: &region, Status: status,
		AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// kendraStatus is a row's Status: the listed status, empty included.
func kendraStatus(status string) *string { return &status }

// scanKendraDataSources stores the data sources of each index with their
// tags, the indices read concurrently and each page written as soon as its
// tags are read (storeEachParentPagesTags).
func scanKendraDataSources(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indices []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, kendraListDataSourcesOp, kendraNotFound, indices,
		func(ctx context.Context, index string, token *string) ([]kendratypes.DataSourceSummary, *string, error) {
			out, err := client.ListDataSources(ctx, &kendra.ListDataSourcesInput{IndexId: &index, NextToken: token, MaxResults: sdkaws.Int32(kendraPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.SummaryItems, out.NextToken, nil
		},
		func(index string, d kendratypes.DataSourceSummary) (*store.Resource, string, string) {
			return kendraChildRow(acct, region, scanID, TypeKendraDataSource, "data sources", "data-source", index, d.Id, d.Name, kendraStatus(string(d.Status)), d)
		}, tags, p)
}

// scanKendraFaqs stores the FAQs of each index with their tags, as
// scanKendraDataSources does.
func scanKendraFaqs(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indices []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, kendraListFaqsOp, kendraNotFound, indices,
		func(ctx context.Context, index string, token *string) ([]kendratypes.FaqSummary, *string, error) {
			out, err := client.ListFaqs(ctx, &kendra.ListFaqsInput{IndexId: &index, NextToken: token, MaxResults: sdkaws.Int32(kendraPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.FaqSummaryItems, out.NextToken, nil
		},
		func(index string, f kendratypes.FaqSummary) (*store.Resource, string, string) {
			return kendraChildRow(acct, region, scanID, TypeKendraFaq, "FAQs", "faq", index, f.Id, f.Name, kendraStatus(string(f.Status)), f)
		}, tags, p)
}

// scanKendraAccessControlConfigurations stores the access control
// configurations of each index, the indices read concurrently
// (storeTokenEachParent). The type has no tags.
func scanKendraAccessControlConfigurations(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indices []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, kendraListAccessControlConfigsOp, kendraNotFound, childParentIDs(indices),
		func(ctx context.Context, index string, token *string) ([]kendratypes.AccessControlConfigurationSummary, *string, error) {
			out, err := client.ListAccessControlConfigurations(ctx, &kendra.ListAccessControlConfigurationsInput{IndexId: &index, NextToken: token, MaxResults: sdkaws.Int32(kendraPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AccessControlConfigurations, out.NextToken, nil
		},
		func(index string, a kendratypes.AccessControlConfigurationSummary) (*store.Resource, string, string) {
			return kendraChildRow(acct, region, scanID, TypeKendraAccessControlConfiguration, "access control configurations", "access-control-configuration", index, a.Id, nil, nil, a)
		})
}

// kendraExperience is an experience stored this scan: its index, its id
// (childParent.id) and its row's NativeID (childParent.arn).
type kendraExperience struct {
	indexID string
	childParent
}

// scanKendraExperiences stores the experiences of each index, the indices
// read concurrently (storeTokenEachParent), and returns every one built. A
// failed write stops the scan (runHostPhases), so none is read from after
// it. The type has no tags.
func scanKendraExperiences(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indices []childParent) ([]kendraExperience, int, int, error) {
	var exps []kendraExperience
	total, inserted, err := storeTokenEachParent(ctx, st, acct, region, kendraListExperiencesOp, kendraNotFound, childParentIDs(indices),
		func(ctx context.Context, index string, token *string) ([]kendratypes.ExperiencesSummary, *string, error) {
			out, err := client.ListExperiences(ctx, &kendra.ListExperiencesInput{IndexId: &index, NextToken: token, MaxResults: sdkaws.Int32(kendraPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.SummaryItems, out.NextToken, nil
		},
		func(index string, e kendratypes.ExperiencesSummary) (*store.Resource, string, string) {
			r, why, key := kendraChildRow(acct, region, scanID, TypeKendraExperience, "experiences", "experience", index, e.Id, e.Name, kendraStatus(string(e.Status)), e)
			if r != nil {
				exps = append(exps, kendraExperience{indexID: index, childParent: childParent{id: sv(e.Id), arn: r.NativeID}})
			}
			return r, why, key
		})
	return exps, total, inserted, err
}

// scanKendraFeaturedResultsSets stores the featured results sets of each
// index with their tags, as scanKendraDataSources does. The op has no SDK
// paginator; eachTokenPage follows its NextToken.
func scanKendraFeaturedResultsSets(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indices []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, kendraListFeaturedResultsSetsOp, kendraNotFound, indices,
		func(ctx context.Context, index string, token *string) ([]kendratypes.FeaturedResultsSetSummary, *string, error) {
			out, err := client.ListFeaturedResultsSets(ctx, &kendra.ListFeaturedResultsSetsInput{IndexId: &index, NextToken: token, MaxResults: sdkaws.Int32(kendraPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.FeaturedResultsSetSummaryItems, out.NextToken, nil
		},
		func(index string, f kendratypes.FeaturedResultsSetSummary) (*store.Resource, string, string) {
			return kendraChildRow(acct, region, scanID, TypeKendraFeaturedResultsSet, "featured results sets", "featured-results-set", index,
				f.FeaturedResultsSetId, f.FeaturedResultsSetName, kendraStatus(string(f.Status)), f)
		}, tags, p)
}

// scanKendraQuerySuggestionsBlockLists stores the query suggestions block
// lists of each index with their tags, as scanKendraDataSources does.
func scanKendraQuerySuggestionsBlockLists(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indices []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, kendraListBlockListsOp, kendraNotFound, indices,
		func(ctx context.Context, index string, token *string) ([]kendratypes.QuerySuggestionsBlockListSummary, *string, error) {
			out, err := client.ListQuerySuggestionsBlockLists(ctx, &kendra.ListQuerySuggestionsBlockListsInput{IndexId: &index, NextToken: token, MaxResults: sdkaws.Int32(kendraPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.BlockListSummaryItems, out.NextToken, nil
		},
		func(index string, b kendratypes.QuerySuggestionsBlockListSummary) (*store.Resource, string, string) {
			return kendraChildRow(acct, region, scanID, TypeKendraQuerySuggestionsBlockList, "query suggestions block lists", "query-suggestions-block-list", index, b.Id, b.Name, kendraStatus(string(b.Status)), b)
		}, tags, p)
}

// scanKendraThesauri stores the thesauri of each index with their tags, as
// scanKendraDataSources does.
func scanKendraThesauri(ctx context.Context, client kendraAPI, acct *account, region string, st *store.Store, scanID string, indices []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, kendraListThesauriOp, kendraNotFound, indices,
		func(ctx context.Context, index string, token *string) ([]kendratypes.ThesaurusSummary, *string, error) {
			out, err := client.ListThesauri(ctx, &kendra.ListThesauriInput{IndexId: &index, NextToken: token, MaxResults: sdkaws.Int32(kendraPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ThesaurusSummaryItems, out.NextToken, nil
		},
		func(index string, th kendratypes.ThesaurusSummary) (*store.Resource, string, string) {
			return kendraChildRow(acct, region, scanID, TypeKendraThesaurus, "thesauri", "thesaurus", index, th.Id, th.Name, kendraStatus(string(th.Status)), th)
		}, tags, p)
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
