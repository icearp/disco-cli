package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/invoicing"
	invtypes "github.com/aws/aws-sdk-go-v2/service/invoicing/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

const (
	invoicingListUnitsOp = "invoicing:ListInvoiceUnits"
	invoicingListPrefsOp = "invoicing:ListProcurementPortalPreferences"
	invoicingTagsOp      = "invoicing:ListTagsForResource"
	// invoicingUnitsPageCap is the MaxResults range maximum (1-500) the model
	// gives ListInvoiceUnits.
	invoicingUnitsPageCap int32 = 500
	// invoicingPrefsPageCap is the MaxResults range maximum (1-100) the model
	// gives ListProcurementPortalPreferences.
	invoicingPrefsPageCap int32 = 100
)

func init() {
	registerType(restype.Descriptor{Type: TypeInvoicingInvoiceUnit, Service: "invoicing"})
	registerType(restype.Descriptor{Type: TypeInvoicingProcurementPortalPreference, Service: "invoicing"})
	registerService(serviceEntry{
		name:   "aws:invoicing",
		global: true,
		fn:     scanInvoicing,
	})
}

// invoicingAPI is the narrow set of Invoicing operations called by
// scanInvoicingIn's phases.
type invoicingAPI interface {
	ListInvoiceUnits(context.Context, *invoicing.ListInvoiceUnitsInput, ...func(*invoicing.Options)) (*invoicing.ListInvoiceUnitsOutput, error)
	ListProcurementPortalPreferences(context.Context, *invoicing.ListProcurementPortalPreferencesInput, ...func(*invoicing.Options)) (*invoicing.ListProcurementPortalPreferencesOutput, error)
	ListTagsForResource(context.Context, *invoicing.ListTagsForResourceInput, ...func(*invoicing.Options)) (*invoicing.ListTagsForResourceOutput, error)
}

// scanInvoicing discovers Invoicing invoice units and procurement portal
// preferences. Service is global; called once, in its home region
// (homeRegion).
func scanInvoicing(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (int, int, error) {
	region, err := acct.homeRegion("invoicing")
	if err != nil {
		return 0, 0, err
	}
	client := invoicing.NewFromConfig(acct.cfg, func(o *invoicing.Options) { o.Region = region })
	return scanInvoicingIn(ctx, client, acct, region, st, scanID)
}

// scanInvoicingIn stores the invoice units and the procurement portal
// preferences, each with its tags (invoicingTags), through client, which
// calls region. The listings run as phases, one at a time and in that order:
// a phase's error does not stop the later one; a failed write and ctx's end
// do, and so does a first phase that stores nothing and fails only with
// NXDOMAIN (runHostPhases). Each listing is written a page at a time, and
// the tag reads' denials are one warning for the whole scan. The tag reads
// go through the same client, so they reach the listings' endpoint.
//
// A member account's payer-only refusal of a listing's first page reads as
// an empty listing and does not stop the other phase: ListInvoiceUnits is
// observed to be payer-only, but nothing verifies that
// ListProcurementPortalPreferences is, so neither phase gates the other. The
// scan reports not-entitled only when every phase was refused, leaving
// nothing to store or fail; otherwise it returns the counts, because the
// dispatcher discards a sentinel's counts.
func scanInvoicingIn(ctx context.Context, client invoicingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var payerOnly invoicingPayerOnly
	tags := invoicingTags(client).sharingDenials()
	phases := []hostPhase{
		{op: invoicingListUnitsOp, run: func(p *phaseTotals) bool {
			return scanInvoicingInvoiceUnits(ctx, client, acct, region, st, scanID, &payerOnly, tags, p)
		}},
		{op: invoicingListPrefsOp, run: func(p *phaseTotals) bool {
			return scanInvoicingProcurementPortalPreferences(ctx, client, acct, region, st, scanID, &payerOnly, tags, p)
		}},
	}
	total, inserted, err := runHostPhases(ctx, st, acct, region, "invoicing", 0, phases)
	if payerOnly.refusals == len(phases) {
		return 0, 0, markServiceNotEntitled(payerOnly.refusal)
	}
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// invoicingPayerOnly counts the listings whose first page was refused as
// payer-only (isPayerAccountOnly), keeping a refusal for the not-entitled
// error. Any refusal will do: the dispatcher reports a not-entitled service by
// its status alone and drops the error's text (reportServiceOutcome). The
// phases run one at a time, so it needs no lock.
type invoicingPayerOnly struct {
	refusals int
	refusal  error
}

// refusedFirstPage reports whether err is the payer-only refusal of a
// listing's first page (token nil), and counts it. The same refusal of a
// later page follows a page the account was served, so it is not counted and
// the listing returns it as an error.
func (r *invoicingPayerOnly) refusedFirstPage(token *string, err error) bool {
	if token != nil || !isPayerAccountOnly(err) {
		return false
	}
	r.refusals++
	r.refusal = err
	return true
}

// invoicingTags reads an invoice unit's or a procurement portal preference's
// tags with ListTagsForResource, sent the row's NativeID: the ARN its listed
// element carries. The Service Reference lists invoice-unit and
// procurement-portal-preference under ListTagsForResource and TagResource,
// each with aws:ResourceTag as a condition key; the op's ResourceArn pattern
// accepts any invoicing ARN of an account; CreateInvoiceUnit and
// CreateProcurementPortalPreference take ResourceTags. Neither listed element
// carries tags. The op returns every tag at once. ResourceNotFoundException,
// its modeled not-found, drops a resource deleted after it was listed.
func invoicingTags(client invoicingAPI) rowTagReader {
	return rowTagReader{
		op:       invoicingTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &invoicing.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.ResourceTags, func(t invtypes.ResourceTag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanInvoicingInvoiceUnits stores the invoice units, keyed by the ARN each
// listed unit carries, each page written as soon as its rows' tags are read.
// A first page payerOnly counts is read as an empty listing. It reports
// whether a write failed, after which the scan must stop.
func scanInvoicingInvoiceUnits(ctx context.Context, client invoicingAPI, acct *account, region string, st *store.Store, scanID string, payerOnly *invoicingPayerOnly, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, invoicingListUnitsOp,
		func(ctx context.Context, token *string) ([]invtypes.InvoiceUnit, *string, error) {
			out, err := client.ListInvoiceUnits(ctx, &invoicing.ListInvoiceUnitsInput{MaxResults: sdkaws.Int32(invoicingUnitsPageCap), NextToken: token})
			if payerOnly.refusedFirstPage(token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.InvoiceUnits, out.NextToken, nil
		},
		func(u invtypes.InvoiceUnit) (*store.Resource, string, string) {
			return arnRow("invoice units", u.InvoiceUnitArn, u.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInvoicingInvoiceUnit, NativeID: sv(u.InvoiceUnitArn),
				Name: u.Name, Region: regionGlobal,
				AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanInvoicingProcurementPortalPreferences stores every procurement portal
// preference, keyed by the ARN the summary returns, each page written as soon
// as its rows' tags are read. The summary carries no
// ProcurementPortalSharedSecret (only the full preference from
// GetProcurementPortalPreference does), so nothing needs redacting. The
// summary has no name, and its two statuses (e-invoice delivery and purchase
// order retrieval) describe separate features, so neither Name nor Status is
// set; a summary without an ARN is warned about by its buyer identifier.
//
// ListProcurementPortalPreferences arrived in SDK v1.9.0 (2025-11-21), almost
// a year after the service (v1.0.0, 2024-12-02), and is a preview API. On the
// first page, the shapes an unrouted operation takes (isOpNotDeployed) are a
// sub-feature gap, read as an empty listing. The op models no not-found
// error, so a 404 on its unfiltered first call means the op is absent. A
// first page payerOnly counts is read as an empty listing too. Once a page
// has succeeded the op is deployed: a later error of any shape except a
// denial is returned, after the rows listed so far are stored. It reports
// whether a write failed, after which the scan must stop.
func scanInvoicingProcurementPortalPreferences(ctx context.Context, client invoicingAPI, acct *account, region string, st *store.Store, scanID string, payerOnly *invoicingPayerOnly, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, invoicingListPrefsOp,
		firstPageGapPages(isOpNotDeployed, func(ctx context.Context, token *string) ([]invtypes.ProcurementPortalPreferenceSummary, *string, error) {
			out, err := client.ListProcurementPortalPreferences(ctx, &invoicing.ListProcurementPortalPreferencesInput{MaxResults: sdkaws.Int32(invoicingPrefsPageCap), NextToken: token})
			if payerOnly.refusedFirstPage(token, err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.ProcurementPortalPreferences, out.NextToken, nil
		}),
		func(pr invtypes.ProcurementPortalPreferenceSummary) (*store.Resource, string, string) {
			return arnRow("procurement portal preferences", pr.ProcurementPortalPreferenceArn, pr.BuyerIdentifier, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInvoicingProcurementPortalPreference, NativeID: sv(pr.ProcurementPortalPreferenceArn),
				Region: regionGlobal, CreatedAt: tp(pr.CreateDate),
				AttributesJSON: mustJSON(pr), DiscoveredBy: scanID,
			})
		}, tags, p)
}
