package aws

import (
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appfabric"
	aftypes "github.com/aws/aws-sdk-go-v2/service/appfabric/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppFabricAppBundle, Service: "appfabric"})
	registerType(restype.Descriptor{Type: TypeAppFabricAppAuthorization, Service: "appfabric"})
	registerType(restype.Descriptor{Type: TypeAppFabricIngestion, Service: "appfabric"})
	registerType(restype.Descriptor{Type: TypeAppFabricIngestionDestination, Service: "appfabric"})
	registerService(serviceEntry{
		name: "aws:appfabric",
		fn:   scanAppFabric,
	})
}

type appFabricAPI interface {
	ListAppBundles(context.Context, *appfabric.ListAppBundlesInput, ...func(*appfabric.Options)) (*appfabric.ListAppBundlesOutput, error)
	GetAppBundle(context.Context, *appfabric.GetAppBundleInput, ...func(*appfabric.Options)) (*appfabric.GetAppBundleOutput, error)
	ListAppAuthorizations(context.Context, *appfabric.ListAppAuthorizationsInput, ...func(*appfabric.Options)) (*appfabric.ListAppAuthorizationsOutput, error)
	ListIngestions(context.Context, *appfabric.ListIngestionsInput, ...func(*appfabric.Options)) (*appfabric.ListIngestionsOutput, error)
	ListIngestionDestinations(context.Context, *appfabric.ListIngestionDestinationsInput, ...func(*appfabric.Options)) (*appfabric.ListIngestionDestinationsOutput, error)
	GetIngestionDestination(context.Context, *appfabric.GetIngestionDestinationInput, ...func(*appfabric.Options)) (*appfabric.GetIngestionDestinationOutput, error)
	ListTagsForResource(context.Context, *appfabric.ListTagsForResourceInput, ...func(*appfabric.Options)) (*appfabric.ListTagsForResourceOutput, error)
}

const (
	afTagsOp = "appfabric:ListTagsForResource"
	// afPageCap is the maxResults maximum of every AppFabric listing read
	// here.
	afPageCap = 100
)

// afGone is the not-found code that ListTagsForResource, GetAppBundle and
// GetIngestionDestination model for a resource deleted since it was listed,
// and that every per-bundle and per-ingestion listing models for a parent
// deleted so.
var afGone = []string{"ResourceNotFoundException"}

// ingestionDestAttrs embeds the native IngestionDestination and flattens its
// nested destination union (S3 bucket / Firehose stream) into lowercase sibling
// keys the resolver can read without re-walking the union shape.
type ingestionDestAttrs struct {
	aftypes.IngestionDestination
	S3BucketName       *string `json:"s3BucketName,omitempty"`
	FirehoseStreamName *string `json:"firehoseStreamName,omitempty"`
}

// scanAppFabric discovers the AppFabric resources of one region: the app
// bundles, enriched by GetAppBundle (the customer managed KMS key), each
// bundle's app authorizations and ingestions, and each ingestion's
// destinations, enriched by GetIngestionDestination (the S3 or Firehose
// target). Every listing runs as its own phase, one at a time: a phase's
// error does not stop the later ones; a failed write and ctx's end do
// (runHostPhases). The child phases read the parents stored this scan, those
// parents concurrently. Every type has its tags read per row (afTags), whose
// denials are one warning for the whole scan.
//
// App authorizations are read from the list summary, never
// GetAppAuthorization, to avoid pulling the OAuth/API-key credential body
// into the store.
func scanAppFabric(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := appfabric.NewFromConfig(acct.cfg, func(o *appfabric.Options) { o.Region = region })
	tags := afTags(client).sharingDenials()
	var (
		bundles    []childParent
		ingestions []childParent
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "appfabric", 0, []hostPhase{
		{op: "appfabric:ListAppBundles", run: func(p *phaseTotals) (failed bool) {
			bundles, failed = scanAppFabricBundles(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "appfabric:ListAppAuthorizations", run: func(p *phaseTotals) bool {
			return scanAppFabricAuthorizations(ctx, client, acct, region, st, scanID, bundles, tags, p)
		}},
		{op: "appfabric:ListIngestions", run: func(p *phaseTotals) (failed bool) {
			ingestions, failed = scanAppFabricIngestions(ctx, client, acct, region, st, scanID, bundles, tags, p)
			return failed
		}},
		{op: "appfabric:ListIngestionDestinations", run: func(p *phaseTotals) bool {
			return scanAppFabricDestinations(ctx, client, acct, region, st, scanID, ingestions, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// afTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The Service Reference lists all
// four types read here under ListTagsForResource (appauthorization,
// appbundle, ingestion, ingestiondestination), and no List or Get response
// read here carries tags. The op does not page. ResourceNotFoundException,
// its modeled not-found, drops a resource deleted after it was listed.
func afTags(client appFabricAPI) rowTagReader {
	return rowTagReader{
		op:       afTagsOp,
		notFound: afGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &appfabric.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t aftypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// afRow is the row of a listed element stored under its ARN, or, when the
// element has no ARN, no row and the reason it is warned about (arnRow).
// what names the listing's elements, name the element in that warning.
func afRow(acct *account, region, scanID, typ, what string, arn, name, status *string, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: &region, Status: status,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanAppFabricBundles stores the app bundles with their tags, each page
// written as soon as its rows are read, and returns the stored ones, the
// parents of the per-bundle phases, each keyed by its ARN. A bundle is
// stored as its GetAppBundle body; one whose Get fails other than with
// ResourceNotFoundException is stored as its listed element (readRowDetails).
// It reports whether a write failed, after which the scan must stop.
func scanAppFabricBundles(ctx context.Context, client appFabricAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	var bundles []childParent
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, "appfabric:ListAppBundles",
		func(ctx context.Context, token *string) ([]aftypes.AppBundleSummary, *string, error) {
			out, err := client.ListAppBundles(ctx, &appfabric.ListAppBundlesInput{MaxResults: sdkaws.Int32(afPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AppBundleSummaryList, out.NextToken, nil
		},
		func(b aftypes.AppBundleSummary) (*store.Resource, string, string) {
			return afRow(acct, region, scanID, TypeAppFabricAppBundle, "app bundles", b.Arn, nil, nil, b)
		},
		tokenPageSteps{
			details: rowDetailReader{op: "appfabric:GetAppBundle", what: "app bundle(s)", notFound: afGone, get: func(ctx context.Context, r *store.Resource) (any, error) {
				arn := r.NativeID
				got, err := client.GetAppBundle(ctx, &appfabric.GetAppBundleInput{AppBundleIdentifier: &arn})
				if err != nil || got.AppBundle == nil {
					return nil, err
				}
				return got.AppBundle, nil
			}},
			stored: func(rows []*store.Resource) {
				for _, r := range rows {
					bundles = append(bundles, childParent{id: r.NativeID, arn: r.NativeID})
				}
			},
		},
		tags, p)
	if failed {
		return nil, true
	}
	return bundles, false
}

// scanAppFabricAuthorizations stores each bundle's app authorizations with
// their tags, named by their app, each page written as soon as its tags are
// read (storeEachParentPagesTags); one bundle's failure costs the others none
// of theirs.
func scanAppFabricAuthorizations(ctx context.Context, client appFabricAPI, acct *account, region string, st *store.Store, scanID string, bundles []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentPagesTags(ctx, st, acct, region, "appfabric:ListAppAuthorizations", afGone, bundles,
		func(ctx context.Context, bundle string, token *string) ([]aftypes.AppAuthorizationSummary, *string, error) {
			out, err := client.ListAppAuthorizations(ctx, &appfabric.ListAppAuthorizationsInput{AppBundleIdentifier: &bundle, MaxResults: sdkaws.Int32(afPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AppAuthorizationSummaryList, out.NextToken, nil
		},
		func(_ string, a aftypes.AppAuthorizationSummary) (*store.Resource, string, string) {
			status := string(a.Status)
			return afRow(acct, region, scanID, TypeAppFabricAppAuthorization, "app authorizations", a.AppAuthorizationArn, a.App, &status, a)
		},
		tags, p)
}

// afListedIngestion is an ingestion with bundle, the app bundle ARN its
// ListIngestions call was sent. Its destination calls are sent that same
// ARN, not one parsed out of the ingestion's own ARN, so they name the
// bundle exactly as the listing did.
type afListedIngestion struct {
	bundle string
	in     aftypes.IngestionSummary
}

// scanAppFabricIngestions stores each bundle's ingestions with their tags,
// named by their app, each page written as soon as its tags are read, and
// returns the stored ones, the parents of the destination phase, each keyed
// by the bundle it was listed under (storeNestedParentPagesTags). One
// bundle's failure costs the others none of theirs. It reports whether a
// write failed, after which the scan must stop.
func scanAppFabricIngestions(ctx context.Context, client appFabricAPI, acct *account, region string, st *store.Store, scanID string, bundles []childParent, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	// The key is the listing's bundle, never empty, so no ingestion is warned
	// about as stored without its destinations.
	return storeNestedParentPagesTags(ctx, st, acct, region, "appfabric:ListIngestions", "ingestions", "app bundle", afGone, bundles,
		func(ctx context.Context, bundle string, token *string) ([]afListedIngestion, *string, error) {
			out, err := client.ListIngestions(ctx, &appfabric.ListIngestionsInput{AppBundleIdentifier: &bundle, MaxResults: sdkaws.Int32(afPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			listed := make([]afListedIngestion, len(out.Ingestions))
			for i, in := range out.Ingestions {
				listed[i] = afListedIngestion{bundle: bundle, in: in}
			}
			return listed, out.NextToken, nil
		},
		func(_ string, l afListedIngestion) (*store.Resource, string, string) {
			status := string(l.in.State)
			return afRow(acct, region, scanID, TypeAppFabricIngestion, "ingestions", l.in.Arn, l.in.App, &status, l.in)
		},
		func(l afListedIngestion) string { return l.bundle },
		tags, p)
}

// scanAppFabricDestinations lists the destinations of each ingestion, whose
// id is the app bundle it was listed under, the ingestions concurrently
// (readEachKey), and stores each as its
// GetIngestionDestination body with the S3 bucket or Firehose stream lifted
// out (flattenIngestionDest), with its tags. A destination whose Get fails
// other than with ResourceNotFoundException is stored as its listed element
// (readRowDetails). One ingestion's failure costs the others none of theirs:
// an ingestion deleted since it was listed is skipped, a denial warns once,
// any other error is returned after the rows are stored. The rows are held
// until one write: no shared per-parent helper writes page by page with Get
// bodies. It reports whether the write failed, after which the scan must
// stop.
func scanAppFabricDestinations(ctx context.Context, client appFabricAPI, acct *account, region string, st *store.Store, scanID string, ingestions []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "appfabric:ListIngestionDestinations"
	reads, stopped := readEachKey(ctx, ingestions, func(ctx context.Context, ing childParent) parentItems[aftypes.IngestionDestinationSummary] {
		return readParentPages(ctx, ing.arn, func(ctx context.Context, _ string, token *string) ([]aftypes.IngestionDestinationSummary, *string, error) {
			out, err := client.ListIngestionDestinations(ctx, &appfabric.ListIngestionDestinationsInput{
				AppBundleIdentifier: &ing.id, IngestionIdentifier: &ing.arn, MaxResults: sdkaws.Int32(afPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.IngestionDestinations, out.NextToken, nil
		})
	})
	// Written only here, before the Gets, each of which reads its own row's.
	parentOf := map[*store.Resource]childParent{}
	out := parentOutcomes{op: op, notFound: afGone}
	for i, read := range reads {
		ing := ingestions[i]
		addParentRows(&out, ing.arn, read, func(_ string, d aftypes.IngestionDestinationSummary) (*store.Resource, string, string) {
			r, why, key := afRow(acct, region, scanID, TypeAppFabricIngestionDestination, "ingestion destinations", d.Arn, nil, nil, d)
			if r != nil {
				parentOf[r] = ing
			}
			return r, why, key
		})
	}
	out.stopped(stopped)
	d := &detailedRows{getOp: "appfabric:GetIngestionDestination", what: "ingestion destination(s)", notFound: afGone, tags: tags}
	var warn func()
	out.rows, warn = d.before(ctx, st, acct, region, func(ctx context.Context, r *store.Resource) (any, error) {
		ing, dest := parentOf[r], r.NativeID
		got, err := client.GetIngestionDestination(ctx, &appfabric.GetIngestionDestinationInput{
			AppBundleIdentifier: &ing.id, IngestionIdentifier: &ing.arn, IngestionDestinationIdentifier: &dest,
		})
		if err != nil || got.IngestionDestination == nil {
			return nil, err
		}
		return flattenIngestionDest(got.IngestionDestination), nil
	})(out.rows)
	total, inserted, err := out.store(st, acct, region)
	if !errors.Is(err, store.ErrStoreWrite) {
		warn()
	}
	return d.addPhase(p, op, total, inserted, err)
}

// flattenIngestionDest embeds the native destination body and lifts the S3
// bucket / Firehose stream out of the nested union into flat sibling keys
// (union interfaces aren't JSON-rehydratable; the resolver reads only these
// flattened fields).
func flattenIngestionDest(dest *aftypes.IngestionDestination) ingestionDestAttrs {
	out := ingestionDestAttrs{IngestionDestination: *dest}
	if cfg, ok := dest.DestinationConfiguration.(*aftypes.DestinationConfigurationMemberAuditLog); ok {
		switch dst := cfg.Value.Destination.(type) {
		case *aftypes.DestinationMemberS3Bucket:
			out.S3BucketName = dst.Value.BucketName
		case *aftypes.DestinationMemberFirehoseStream:
			out.FirehoseStreamName = dst.Value.StreamName
		}
	}
	return out
}
