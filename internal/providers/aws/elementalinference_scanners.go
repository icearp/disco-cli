package aws

import (
	"context"
	"slices"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/elementalinference"
	eitypes "github.com/aws/aws-sdk-go-v2/service/elementalinference/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

const (
	eiListFeedsOp        = "elemental-inference:ListFeeds"
	eiListDictionariesOp = "elemental-inference:ListDictionaries"
	eiTagsOp             = "elemental-inference:ListTagsForResource"
	// eiPageCap is the maxResults range maximum (1-100) the model gives
	// ListFeeds and ListDictionaries.
	eiPageCap int32 = 100
	// eiThrottleCode is Elemental Inference's modeled throttle (HTTP 429,
	// marked retryable in the model), which every op disco calls lists. The
	// SDK retryer recognises neither the code (its list has
	// TooManyRequestsException, plural) nor the status.
	eiThrottleCode = "TooManyRequestException"
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
	ListTagsForResource(context.Context, *elementalinference.ListTagsForResourceInput, ...func(*elementalinference.Options)) (*elementalinference.ListTagsForResourceOutput, error)
}

// scanElementalInference discovers Elemental Inference feeds and
// dictionaries, each with its tags (eiTags). Both listings run as phases,
// one at a time and in that order: a phase's error does not stop the later
// one; a failed write and ctx's end do, and so does a feed phase that stores
// nothing and fails only with NXDOMAIN (runHostPhases). Each listing is
// written a page at a time, and the tag reads' denials are one warning for
// the whole scan.
func scanElementalInference(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := elementalinference.NewFromConfig(acct.cfg, eiClientOptions(region))
	tags := eiTags(client).sharingDenials()
	total, err := runHostPhases(ctx, st, acct, region, "elemental-inference", 0, []hostPhase{
		{op: eiListFeedsOp, run: func(p *phaseTotals) bool {
			return scanEIFeeds(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: eiListDictionariesOp, run: func(p *phaseTotals) bool {
			return scanEIDictionaries(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// eiClientOptions pins the region and retries eiThrottleCode, which would
// otherwise fail a throttled call, a tag read of the concurrent fan-out
// included, on its first attempt. The code is retried with the retryer's
// standard backoff only: AddWithErrorCodes changes which errors are
// retryable, not which count as throttles, so adaptive mode's client-side
// rate limiting does not learn from it.
func eiClientOptions(region string) func(*elementalinference.Options) {
	return func(o *elementalinference.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, eiThrottleCode)
	}
}

// eiTags reads a feed's or dictionary's tags with ListTagsForResource, sent
// the row's NativeID: the ARN its listed element carries. The Service
// Reference names no resource for ListTagsForResource, but the model's
// ResourceArn pattern for the op accepts feed and dictionary ARNs, the
// Service Reference lists both for TagResource and gives both
// aws:ResourceTag as a condition key, and CreateFeed and CreateDictionary
// take tags. No listed element carries them (GetFeed and GetDictionary do,
// but the scan does not call them). ResourceNotFoundException, the op's
// modeled not-found, drops a resource deleted after it was listed.
func eiTags(client elementalInferenceAPI) rowTagReader {
	return rowTagReader{
		op:       eiTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &elementalinference.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanEIFeeds stores every feed with its tags, each page written as soon as
// its rows' tags are read. It reports whether a write failed, after which
// the scan must stop.
//
// A DELETED feed (DeleteFeed's end state) is dropped; a DELETING one still
// exists and is stored. ARCHIVED is undocumented in the model, and nothing
// says an archived feed is gone, so it is stored too.
func scanEIFeeds(ctx context.Context, client elementalInferenceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, eiListFeedsOp,
		func(ctx context.Context, token *string) ([]eitypes.FeedSummary, *string, error) {
			out, err := client.ListFeeds(ctx, &elementalinference.ListFeedsInput{MaxResults: sdkaws.Int32(eiPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return slices.DeleteFunc(out.Feeds, func(f eitypes.FeedSummary) bool { return f.Status == eitypes.FeedStatusDeleted }), out.NextToken, nil
		},
		func(f eitypes.FeedSummary) (*store.Resource, string, string) {
			return arnRow("feeds", f.Arn, f.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeElementalInferenceFeed, NativeID: sv(f.Arn),
				Name: f.Name, Status: nonEmptyPtr(string(f.Status)), Region: &region,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanEIDictionaries stores every custom transcription dictionary with its
// tags, keyed by the ARN the summary returns, each page written as soon as
// its rows' tags are read. A summary carries no secret material. A DELETED
// dictionary is dropped; a DELETING one still exists and is stored. It
// reports whether a write failed, after which the scan must stop.
//
// ListDictionaries arrived with smart subtitles (SDK v1.1.0, 2026-05-27), three
// months after the feed API (v1.0.0, 2026-02-24), so a region may serve feeds
// without it. On the first call, the shapes an unrouted operation takes
// (isOpNotDeployed) are a sub-feature gap, skipped silently
// (firstPageGapPages). ListDictionaries models no not-found error, so a 404
// on its unfiltered first call means the op is absent. Once a page has
// succeeded the op is deployed: a later error of any shape except a denial
// is returned, after the rows listed so far are stored.
func scanEIDictionaries(ctx context.Context, client elementalInferenceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, eiListDictionariesOp,
		firstPageGapPages(isOpNotDeployed, func(ctx context.Context, token *string) ([]eitypes.DictionarySummary, *string, error) {
			out, err := client.ListDictionaries(ctx, &elementalinference.ListDictionariesInput{MaxResults: sdkaws.Int32(eiPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return slices.DeleteFunc(out.Dictionaries, func(d eitypes.DictionarySummary) bool { return d.Status == eitypes.DictionaryStatusDeleted }), out.NextToken, nil
		}),
		func(d eitypes.DictionarySummary) (*store.Resource, string, string) {
			return arnRow("dictionaries", d.Arn, d.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeElementalInferenceDictionary, NativeID: sv(d.Arn),
				Name: d.Name, Status: nonEmptyPtr(string(d.Status)), Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}, tags, p)
}
