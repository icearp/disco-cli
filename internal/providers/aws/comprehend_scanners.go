package aws

import (
	"context"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/comprehend"
	comprehendtypes "github.com/aws/aws-sdk-go-v2/service/comprehend/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeComprehendDocumentClassifier, Service: "comprehend"})
	registerType(restype.Descriptor{Type: TypeComprehendEntityRecognizer, Service: "comprehend"})
	registerType(restype.Descriptor{Type: TypeComprehendDocumentClassifierEndpoint, Service: "comprehend"})
	registerType(restype.Descriptor{Type: TypeComprehendEntityRecognizerEndpoint, Service: "comprehend"})
	registerType(restype.Descriptor{Type: TypeComprehendFlywheel, Service: "comprehend"})
	registerType(restype.Descriptor{Type: TypeComprehendDataset, Service: "comprehend"})
	registerService(serviceEntry{
		name: "aws:comprehend",
		fn:   scanComprehend,
	})
}

// isComprehendNotEnabled reports the NotAuthorizedException Comprehend returns
// for an account not subscribed to its custom-model surface in this region
// ("Your account is not authorized to make this call."). NotAuthorizedException
// is in accessDeniedCodes, so without this the phase records an IAM-style
// warning on every scan of every such region.
//
// Per-phase and not markServiceUnavailable: ListFlywheels succeeds in the same
// regions where the three custom-model ops fail, so Comprehend itself IS served
// there — marking the whole service region-unavailable would blank a working
// scanner.
func isComprehendNotEnabled(err error) bool {
	return isAPIErrorWithMessage(err, "NotAuthorizedException", "account is not authorized to make this call")
}

// isComprehendRegionGap reports a listing this region does not serve this
// account: Comprehend's per-region feature gap (InvalidRequestException
// "UNSUPPORTED_OPERATION") or its custom-model surface not enabled
// (isComprehendNotEnabled). Every listing reads it on its first page as an
// empty listing (firstPageGapPages), before the denial check that would
// otherwise claim the NotAuthorizedException.
func isComprehendRegionGap(err error) bool {
	return isAPIErrorWithMessage(err, "InvalidRequestException", "UNSUPPORTED_OPERATION") || isComprehendNotEnabled(err)
}

type comprehendAPI interface {
	ListDocumentClassifiers(context.Context, *comprehend.ListDocumentClassifiersInput, ...func(*comprehend.Options)) (*comprehend.ListDocumentClassifiersOutput, error)
	ListEntityRecognizers(context.Context, *comprehend.ListEntityRecognizersInput, ...func(*comprehend.Options)) (*comprehend.ListEntityRecognizersOutput, error)
	ListEndpoints(context.Context, *comprehend.ListEndpointsInput, ...func(*comprehend.Options)) (*comprehend.ListEndpointsOutput, error)
	ListFlywheels(context.Context, *comprehend.ListFlywheelsInput, ...func(*comprehend.Options)) (*comprehend.ListFlywheelsOutput, error)
	ListDatasets(context.Context, *comprehend.ListDatasetsInput, ...func(*comprehend.Options)) (*comprehend.ListDatasetsOutput, error)
	ListTagsForResource(context.Context, *comprehend.ListTagsForResourceInput, ...func(*comprehend.Options)) (*comprehend.ListTagsForResourceOutput, error)
}

const (
	comprehendTagsOp = "comprehend:ListTagsForResource"
	// comprehendPageCap is the MaxResults maximum of every Comprehend listing
	// read here (MaxResultsInteger, 1-500).
	comprehendPageCap = 500
)

// comprehendNotFound is the not-found code ListDatasets and
// ListTagsForResource model: a flywheel or resource deleted after it was
// listed.
var comprehendNotFound = []string{"ResourceNotFoundException"}

// scanComprehend discovers Comprehend document classifiers, entity
// recognizers, real-time inference endpoints, flywheels and flywheel
// datasets, each with its tags (comprehendTags). Every listing runs as its
// own phase, one at a time and in that order, the datasets last since they
// are listed per flywheel stored: a phase's error does not stop the later
// ones; a failed write and ctx's end do, and so does a first phase that
// stores nothing and fails only with NXDOMAIN, since the region does not
// serve the service (runHostPhases). The tag reads' denials are one warning
// for the whole scan.
func scanComprehend(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := comprehend.NewFromConfig(acct.cfg, func(o *comprehend.Options) { o.Region = region })
	tags := comprehendTags(client).sharingDenials()
	var flywheels []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "comprehend", 0, []hostPhase{
		{op: "comprehend:ListDocumentClassifiers", run: func(p *phaseTotals) bool {
			return scanComprehendDocumentClassifiers(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "comprehend:ListEntityRecognizers", run: func(p *phaseTotals) bool {
			return scanComprehendEntityRecognizers(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "comprehend:ListEndpoints", run: func(p *phaseTotals) bool {
			return scanComprehendEndpoints(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "comprehend:ListFlywheels", run: func(p *phaseTotals) bool {
			var failed bool
			flywheels, failed = scanComprehendFlywheels(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "comprehend:ListDatasets", run: func(p *phaseTotals) bool {
			return scanComprehendDatasets(ctx, client, acct, region, st, scanID, flywheels, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// comprehendTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference lists every
// type stored here as a resource of the op (document-classifier,
// entity-recognizer, both endpoint kinds, flywheel, flywheel-dataset), each
// type's Create input takes Tags, and the op's model pattern (ComprehendArn)
// takes a model version's ARN (…/version/<name>) and a dataset's
// (…/dataset/<name>), the forms the listings return. A model version is its
// own row under its own ARN, so its tags are read for it alone. The op
// returns every tag at once. ResourceNotFoundException, its modeled
// not-found, drops a resource deleted after it was listed.
func comprehendTags(client comprehendAPI) rowTagReader {
	return rowTagReader{
		op:       comprehendTagsOp,
		notFound: comprehendNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &comprehend.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t comprehendtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// comprehendRow is the row of a listed model, endpoint or flywheel, stored
// and named under its ARN with status as its Status; an element without an
// ARN is not stored (arnRow), and what names such elements in the listing's
// warning. These elements carry no name of their own.
func comprehendRow(acct *account, region, scanID, typ, what string, arn *string, status string, element any) (*store.Resource, string, string) {
	label := sv(arn)
	return arnRow(what, arn, nil, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: label,
		Name: &label, Region: &region, Status: &status,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanComprehendDocumentClassifiers stores the document classifiers, one row
// per model version, each page written as soon as its rows' tags are read.
// It reports whether a write failed, after which the scan must stop.
func scanComprehendDocumentClassifiers(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "comprehend:ListDocumentClassifiers",
		firstPageGapPages(isComprehendRegionGap, func(ctx context.Context, token *string) ([]comprehendtypes.DocumentClassifierProperties, *string, error) {
			out, err := client.ListDocumentClassifiers(ctx, &comprehend.ListDocumentClassifiersInput{MaxResults: sdkaws.Int32(comprehendPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DocumentClassifierPropertiesList, out.NextToken, nil
		}),
		func(c comprehendtypes.DocumentClassifierProperties) (*store.Resource, string, string) {
			return comprehendRow(acct, region, scanID, TypeComprehendDocumentClassifier, "document classifiers", c.DocumentClassifierArn, string(c.Status), c)
		},
		tags, p)
}

// scanComprehendEntityRecognizers stores the entity recognizers, one row per
// model version, each page written as soon as its rows' tags are read. It
// reports whether a write failed, after which the scan must stop.
func scanComprehendEntityRecognizers(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "comprehend:ListEntityRecognizers",
		firstPageGapPages(isComprehendRegionGap, func(ctx context.Context, token *string) ([]comprehendtypes.EntityRecognizerProperties, *string, error) {
			out, err := client.ListEntityRecognizers(ctx, &comprehend.ListEntityRecognizersInput{MaxResults: sdkaws.Int32(comprehendPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EntityRecognizerPropertiesList, out.NextToken, nil
		}),
		func(e comprehendtypes.EntityRecognizerProperties) (*store.Resource, string, string) {
			return comprehendRow(acct, region, scanID, TypeComprehendEntityRecognizer, "entity recognizers", e.EntityRecognizerArn, string(e.Status), e)
		},
		tags, p)
}

// scanComprehendEndpoints stores the real-time inference endpoints, each
// page written as soon as its rows' tags are read, split by the model they
// front: a ModelArn containing ":entity-recognizer/" is an
// entity-recognizer-endpoint, otherwise a document-classifier-endpoint. It
// reports whether a write failed, after which the scan must stop.
func scanComprehendEndpoints(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "comprehend:ListEndpoints",
		firstPageGapPages(isComprehendRegionGap, func(ctx context.Context, token *string) ([]comprehendtypes.EndpointProperties, *string, error) {
			out, err := client.ListEndpoints(ctx, &comprehend.ListEndpointsInput{MaxResults: sdkaws.Int32(comprehendPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EndpointPropertiesList, out.NextToken, nil
		}),
		func(e comprehendtypes.EndpointProperties) (*store.Resource, string, string) {
			typ := TypeComprehendDocumentClassifierEndpoint
			if strings.Contains(sv(e.ModelArn), ":entity-recognizer/") {
				typ = TypeComprehendEntityRecognizerEndpoint
			}
			return comprehendRow(acct, region, scanID, typ, "endpoints", e.EndpointArn, string(e.Status), e)
		},
		tags, p)
}

// scanComprehendFlywheels stores the flywheels, each page written as soon as
// its rows' tags are read, and returns those stored, keyed by ARN, so the
// dataset phase lists their datasets without listing the flywheels again.
// It reports whether a write failed, after which the scan must stop.
func scanComprehendFlywheels(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "comprehend:ListFlywheels", "flywheels", "FlywheelArn",
		firstPageGapPages(isComprehendRegionGap, func(ctx context.Context, token *string) ([]comprehendtypes.FlywheelSummary, *string, error) {
			out, err := client.ListFlywheels(ctx, &comprehend.ListFlywheelsInput{MaxResults: sdkaws.Int32(comprehendPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FlywheelSummaryList, out.NextToken, nil
		}),
		func(f comprehendtypes.FlywheelSummary) (*store.Resource, string, string) {
			return comprehendRow(acct, region, scanID, TypeComprehendFlywheel, "flywheels", f.FlywheelArn, string(f.Status), f)
		},
		func(f comprehendtypes.FlywheelSummary) string { return sv(f.FlywheelArn) },
		tags, p)
}

// scanComprehendDatasets stores each flywheel's datasets with their tags and
// records each under its flywheel (storeEachParentTagsUnder): one flywheel's
// failure costs the others none of theirs. ListDatasets is authorized
// against the flywheel resource (Service Reference), so a denial may cover
// only some flywheels and is warned about once. A flywheel deleted before its
// listing was read (ResourceNotFoundException) is skipped silently; one
// deleted mid-listing keeps the datasets already read and is warned about
// once. A first page the region does not serve reads as no datasets
// (isComprehendRegionGap). It reports
// whether a write failed, after which the scan must stop.
func scanComprehendDatasets(ctx context.Context, client comprehendAPI, acct *account, region string, st *store.Store, scanID string, flywheels []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentTagsUnder(ctx, st, acct, region, "comprehend:ListDatasets", comprehendNotFound, flywheels,
		func(ctx context.Context, flywheelARN string, token *string) ([]comprehendtypes.DatasetProperties, *string, error) {
			return firstPageGapPages(isComprehendRegionGap, func(ctx context.Context, token *string) ([]comprehendtypes.DatasetProperties, *string, error) {
				out, err := client.ListDatasets(ctx, &comprehend.ListDatasetsInput{FlywheelArn: &flywheelARN, MaxResults: sdkaws.Int32(comprehendPageCap), NextToken: token})
				if err != nil {
					return nil, nil, err
				}
				return out.DatasetPropertiesList, out.NextToken, nil
			})(ctx, token)
		},
		func(_ string, d comprehendtypes.DatasetProperties) (*store.Resource, string, string) {
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeComprehendDataset, NativeID: sv(d.DatasetArn),
				Name: d.DatasetName, Region: &region, CreatedAt: tp(d.CreationTime),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}
			if status := string(d.Status); status != "" {
				r.Status = &status
			}
			return arnRow("datasets", d.DatasetArn, d.DatasetName, r)
		},
		tags, p)
}
