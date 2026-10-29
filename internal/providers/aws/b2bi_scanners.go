package aws

import (
	"cmp"
	"context"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/b2bi"
	b2bitypes "github.com/aws/aws-sdk-go-v2/service/b2bi/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// b2biARN builds arn:{p}:b2bi:{region}:{acct}:{kind}/{id}. The list ops return
// ids only; the scanner keys rows with it and the resolver rebuilds targets.
func b2biARN(partition, region, acct, kind, id string) string {
	return arnFor(partition, "b2bi", region, acct, kind+"/"+id)
}

func init() {
	registerType(restype.Descriptor{Type: TypeB2BICapability, Service: "b2bi"})
	registerType(restype.Descriptor{Type: TypeB2BIPartnership, Service: "b2bi"})
	registerType(restype.Descriptor{Type: TypeB2BIProfile, Service: "b2bi"})
	registerType(restype.Descriptor{Type: TypeB2BITransformer, Service: "b2bi"})
	registerService(serviceEntry{
		name: "aws:b2bi",
		fn:   scanB2BI,
	})
}

type b2biAPI interface {
	ListCapabilities(context.Context, *b2bi.ListCapabilitiesInput, ...func(*b2bi.Options)) (*b2bi.ListCapabilitiesOutput, error)
	GetCapability(context.Context, *b2bi.GetCapabilityInput, ...func(*b2bi.Options)) (*b2bi.GetCapabilityOutput, error)
	ListPartnerships(context.Context, *b2bi.ListPartnershipsInput, ...func(*b2bi.Options)) (*b2bi.ListPartnershipsOutput, error)
	ListProfiles(context.Context, *b2bi.ListProfilesInput, ...func(*b2bi.Options)) (*b2bi.ListProfilesOutput, error)
	ListTransformers(context.Context, *b2bi.ListTransformersInput, ...func(*b2bi.Options)) (*b2bi.ListTransformersOutput, error)
	ListTagsForResource(context.Context, *b2bi.ListTagsForResourceInput, ...func(*b2bi.Options)) (*b2bi.ListTagsForResourceOutput, error)
}

const (
	b2biListCapabilitiesOp = "b2bi:ListCapabilities"
	b2biGetCapabilityOp    = "b2bi:GetCapability"
	b2biListPartnershipsOp = "b2bi:ListPartnerships"
	b2biListProfilesOp     = "b2bi:ListProfiles"
	b2biListTransformersOp = "b2bi:ListTransformers"
	b2biTagsOp             = "b2bi:ListTagsForResource"
	// b2biPageCap is the MaxResults maximum the model gives every B2BI
	// listing read here.
	b2biPageCap = 100
)

// b2biNotFound is the not-found code GetCapability and ListTagsForResource
// model, for a resource deleted after it was listed.
var b2biNotFound = []string{"ResourceNotFoundException"}

// scanB2BI discovers B2B Data Interchange capabilities, partnerships,
// profiles and transformers, each with its tags (b2biTags). Every listing
// runs as its own phase, one at a time and in that order: a phase's error
// does not stop the later ones; a failed write and ctx's end do, and so does
// a first phase that stores nothing and fails only with NXDOMAIN, since the
// region does not serve the service (runHostPhases). Each listing is written
// a page at a time, and the tag reads' denials are one warning for the whole
// scan. Past gateB2BI's probe, a listing whose first page is refused as
// closed to new customers (isClosedToNewCustomers) is still read as empty,
// silently (firstPageGapPages), as base read every listing; whether closure
// can differ per op on one account is unverified.
func scanB2BI(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := b2bi.NewFromConfig(acct.cfg, func(o *b2bi.Options) { o.Region = region })

	if ferr := gateB2BI(ctx, client); ferr != nil {
		return 0, 0, ferr
	}

	tags := b2biTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "b2bi", 0, []hostPhase{
		{op: b2biListCapabilitiesOp, run: func(p *phaseTotals) bool {
			return scanB2BICapabilities(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: b2biListPartnershipsOp, run: func(p *phaseTotals) bool {
			return scanB2BIPartnerships(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: b2biListProfilesOp, run: func(p *phaseTotals) bool {
			return scanB2BIProfiles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: b2biListTransformersOp, run: func(p *phaseTotals) bool {
			return scanB2BITransformers(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// gateB2BI probes the cheapest list op once. B2BI deploys to a region subset;
// unsupported regions return empty-message AccessDeniedException. B2BI is
// closed to new customers, so the account can't self-enable — short-circuit
// via markServiceNotEntitled so the dispatcher renders (account: not entitled) once.
func gateB2BI(ctx context.Context, client b2biAPI) error {
	mr := int32(1)
	_, err := client.ListProfiles(ctx, &b2bi.ListProfilesInput{MaxResults: &mr})
	if err != nil && isClosedToNewCustomers(err) {
		return markServiceNotEntitled(err)
	}
	return nil
}

// b2biTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN b2biARN builds from the listed id, which is the Service
// Reference's ARN format for each type stored here. The Service Reference
// lists every one of those types on ListTagsForResource, and each type's
// Create op takes Tags. The op returns every tag at once.
// ResourceNotFoundException, its modeled not-found, drops a resource deleted
// after it was listed.
func b2biTags(client b2biAPI) rowTagReader {
	return rowTagReader{
		op:       b2biTagsOp,
		notFound: b2biNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &b2bi.ListTagsForResourceInput{ResourceARN: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t b2bitypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// b2biRow is the row of a listed element of typ, stored under the ARN
// b2biARN builds from kind and the element's id, named name. An element
// without an id is not stored, and what names such elements in the
// listing's warning: one empty id would merge every such element into one
// resource.
func b2biRow(acct *account, region, scanID, typ, kind, what string, id, name, status *string, element any) (*store.Resource, string, string) {
	if sv(id) == "" {
		return nil, what + " have no id", cmp.Or(sv(name), noIdentifier)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: b2biARN(acct.partition(), region, acct.ID, kind, sv(id)),
		Name: name, Region: &region, Status: status,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}, "", ""
}

// scanB2BICapabilities stores the capabilities a page at a time, each row's
// attributes replaced with its GetCapability body, which carries what the
// summary lacks (InstructionsDocuments[].BucketName, the S3 source the
// resolver reads). A capability GetCapability no longer finds was deleted
// since the listing and is dropped; any other failure keeps the
// CapabilitySummary as the row (rowDetailReader). Name comes from the
// CapabilitySummary. It reports whether a write failed, after which the scan
// must stop.
func scanB2BICapabilities(ctx context.Context, client b2biAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	steps := tokenPageSteps{details: rowDetailReader{
		op: b2biGetCapabilityOp, what: "capabilities", notFound: b2biNotFound,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			// The NativeID is b2biARN's ".../capability/{id}", and a
			// capability id holds no "/" (model pattern ^[a-zA-Z0-9_-]+$).
			_, id, _ := strings.Cut(r.NativeID, ":capability/")
			out, err := client.GetCapability(ctx, &b2bi.GetCapabilityInput{CapabilityId: &id})
			if err != nil {
				return nil, err
			}
			return out, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, region, b2biListCapabilitiesOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]b2bitypes.CapabilitySummary, *string, error) {
			out, err := client.ListCapabilities(ctx, &b2bi.ListCapabilitiesInput{MaxResults: sdkaws.Int32(b2biPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Capabilities, out.NextToken, nil
		}),
		func(c b2bitypes.CapabilitySummary) (*store.Resource, string, string) {
			return b2biRow(acct, region, scanID, TypeB2BICapability, "capability", "capabilities", c.CapabilityId, c.Name, nil, c)
		}, steps, tags, p)
}

// scanB2BIPartnerships stores the partnerships, each page written as soon as
// its rows' tags are read. It reports whether a write failed, after which the
// scan must stop.
func scanB2BIPartnerships(ctx context.Context, client b2biAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, b2biListPartnershipsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]b2bitypes.PartnershipSummary, *string, error) {
			out, err := client.ListPartnerships(ctx, &b2bi.ListPartnershipsInput{MaxResults: sdkaws.Int32(b2biPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Partnerships, out.NextToken, nil
		}),
		func(ps b2bitypes.PartnershipSummary) (*store.Resource, string, string) {
			return b2biRow(acct, region, scanID, TypeB2BIPartnership, "partnership", "partnerships", ps.PartnershipId, ps.Name, nil, ps)
		}, tags, p)
}

// scanB2BIProfiles stores the profiles, each page written as soon as its
// rows' tags are read. It reports whether a write failed, after which the
// scan must stop.
func scanB2BIProfiles(ctx context.Context, client b2biAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, b2biListProfilesOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]b2bitypes.ProfileSummary, *string, error) {
			out, err := client.ListProfiles(ctx, &b2bi.ListProfilesInput{MaxResults: sdkaws.Int32(b2biPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Profiles, out.NextToken, nil
		}),
		func(pr b2bitypes.ProfileSummary) (*store.Resource, string, string) {
			return b2biRow(acct, region, scanID, TypeB2BIProfile, "profile", "profiles", pr.ProfileId, pr.Name, nil, pr)
		}, tags, p)
}

// scanB2BITransformers stores the transformers, each page written as soon as
// its rows' tags are read, with the transformer's status as Status. It
// reports whether a write failed, after which the scan must stop.
func scanB2BITransformers(ctx context.Context, client b2biAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, b2biListTransformersOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]b2bitypes.TransformerSummary, *string, error) {
			out, err := client.ListTransformers(ctx, &b2bi.ListTransformersInput{MaxResults: sdkaws.Int32(b2biPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Transformers, out.NextToken, nil
		}),
		func(t b2bitypes.TransformerSummary) (*store.Resource, string, string) {
			status := string(t.Status)
			return b2biRow(acct, region, scanID, TypeB2BITransformer, "transformer", "transformers", t.TransformerId, t.Name, &status, t)
		}, tags, p)
}
