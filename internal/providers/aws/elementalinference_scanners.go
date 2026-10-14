package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/elementalinference"
	eitypes "github.com/aws/aws-sdk-go-v2/service/elementalinference/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeElementalInferenceFeed, Service: "elemental-inference"})
	registerType(restype.Descriptor{Type: TypeElementalInferenceDictionary, Service: "elemental-inference"})
	registerService(serviceEntry{
		name: "aws:elemental-inference",
		fn:   scanElementalInference,
	})
}

// elementalInferenceAPI is the narrow set of Elemental Inference operations
// called by scanElementalInference's phases.
type elementalInferenceAPI interface {
	ListFeeds(context.Context, *elementalinference.ListFeedsInput, ...func(*elementalinference.Options)) (*elementalinference.ListFeedsOutput, error)
	ListDictionaries(context.Context, *elementalinference.ListDictionariesInput, ...func(*elementalinference.Options)) (*elementalinference.ListDictionariesOutput, error)
}

// scanElementalInference discovers Elemental Inference feeds and dictionaries.
//
// Phases run in order and stop at the first hard error, returning the counts
// each phase reports, the failing phase's included. Dictionaries are the newer
// API surface and run last, so their failure cannot cost the feeds.
func scanElementalInference(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := elementalinference.NewFromConfig(acct.cfg, func(o *elementalinference.Options) { o.Region = region })
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanEIFeeds(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanEIDictionaries(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total + t, inserted + i, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanEIFeeds stores every feed. A denial warns and an error of any other
// shape is returned; either way the rows listed before it are stored.
//
// A DELETED feed (DeleteFeed's end state) is dropped; a DELETING one still
// exists and is stored. ARCHIVED is undocumented in the model, and nothing
// says an archived feed is gone, so it is stored too.
func scanEIFeeds(ctx context.Context, client elementalInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "elemental-inference:ListFeeds"
	pager := elementalinference.NewListFeedsPaginator(client, &elementalinference.ListFeedsInput{},
		func(o *elementalinference.ListFeedsPaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
	var listErr error
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, op, acct.ID, region, perr)
			} else {
				listErr = fmt.Errorf("%s: %w", op, perr)
			}
			break
		}
		for _, f := range out.Feeds {
			arn := sv(f.Arn)
			if arn == "" || f.Status == eitypes.FeedStatusDeleted {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeElementalInferenceFeed, NativeID: arn,
				Name: f.Name, Status: nonEmptyPtr(string(f.Status)), Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	t, i, uerr := upsertBatch(st, batch, "elemental-inference feeds")
	return t, i, errors.Join(listErr, uerr)
}

// scanEIDictionaries stores every custom transcription dictionary, keyed by
// the ARN the summary returns. A summary carries no secret material. A
// DELETED dictionary is dropped; a DELETING one still exists and is stored.
//
// ListDictionaries arrived with smart subtitles (SDK v1.1.0, 2026-05-27), three
// months after the feed API (v1.0.0, 2026-02-24), so a region may serve feeds
// without it. On the first call, the shapes an unrouted operation takes
// (isOpNotDeployed) are a sub-feature gap, skipped silently. ListDictionaries
// models no not-found error, so a 404 on its unfiltered first call means the
// op is absent. Once a page has succeeded the op is deployed: a later error of
// any shape except a denial is returned, after the rows listed so far are
// stored.
func scanEIDictionaries(ctx context.Context, client elementalInferenceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "elemental-inference:ListDictionaries"
	pager := elementalinference.NewListDictionariesPaginator(client, &elementalinference.ListDictionariesInput{},
		func(o *elementalinference.ListDictionariesPaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
	var listErr error
	listed := false
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			switch {
			case !listed && isOpNotDeployed(perr):
				return 0, 0, nil
			case isAccessDenied(perr):
				_ = skipIfAccessDenied(st, op, acct.ID, region, perr)
			default:
				listErr = fmt.Errorf("%s: %w", op, perr)
			}
			break
		}
		listed = true
		for _, d := range out.Dictionaries {
			arn := sv(d.Arn)
			if arn == "" || d.Status == eitypes.DictionaryStatusDeleted {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeElementalInferenceDictionary, NativeID: arn,
				Name: d.Name, Status: nonEmptyPtr(string(d.Status)), Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	t, i, uerr := upsertBatch(st, batch, "elemental-inference dictionaries")
	return t, i, errors.Join(listErr, uerr)
}
