package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/invoicing"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
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
// scanInvoicing's phases.
type invoicingAPI interface {
	ListInvoiceUnits(context.Context, *invoicing.ListInvoiceUnitsInput, ...func(*invoicing.Options)) (*invoicing.ListInvoiceUnitsOutput, error)
	ListProcurementPortalPreferences(context.Context, *invoicing.ListProcurementPortalPreferencesInput, ...func(*invoicing.Options)) (*invoicing.ListProcurementPortalPreferencesOutput, error)
}

// scanInvoicing discovers Invoicing invoice units and procurement portal
// preferences. Service is global; gate to us-east-1 to avoid duplicate scans
// across regions.
//
// Phases run in order and stop at the first hard error, returning the counts
// each phase reports, the failing phase's included. Procurement portal
// preferences are a preview API newer than the service, so they run last and
// their failure cannot cost the invoice units.
//
// A member account's payer-only rejection is a per-phase not-entitled result,
// not a hard error, and does not stop the loop: ListInvoiceUnits is observed
// to be payer-only, but nothing verifies that ListProcurementPortalPreferences
// is, so neither phase gates the other. The scan reports not-entitled only
// when every phase was; otherwise it returns nil with the counts, because the
// dispatcher discards a sentinel's counts.
func scanInvoicing(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := invoicing.NewFromConfig(acct.cfg, func(o *invoicing.Options) { o.Region = region })
	phases := []func() (int, int, error){
		func() (int, int, error) { return scanInvoicingInvoiceUnits(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanInvoicingProcurementPortalPreferences(ctx, client, acct, region, st, scanID)
		},
	}
	var notEntitled error
	notEntitledPhases := 0
	for _, phase := range phases {
		t, i, perr := phase()
		if errors.Is(perr, errServiceNotEntitled) {
			if notEntitled == nil {
				notEntitled = perr
			}
			notEntitledPhases++
			continue
		}
		if perr != nil {
			return total + t, inserted + i, perr
		}
		total += t
		inserted += i
	}
	if notEntitledPhases == len(phases) {
		return 0, 0, notEntitled
	}
	return total, inserted, nil
}

func scanInvoicingInvoiceUnits(ctx context.Context, client invoicingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListInvoiceUnits(ctx, &invoicing.ListInvoiceUnitsInput{NextToken: nextToken})
		if err != nil {
			if isPayerAccountOnly(err) {
				return 0, 0, markServiceNotEntitled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "invoicing:ListInvoiceUnits", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("invoicing:ListInvoiceUnits: %w", err)
		}
		for _, u := range out.InvoiceUnits {
			arn := sv(u.InvoiceUnitArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInvoicingInvoiceUnit, NativeID: arn,
				Name: u.Name, Region: regionGlobal,
				AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "invoicing invoice-units")
}

// scanInvoicingProcurementPortalPreferences stores every procurement portal
// preference, keyed by the ARN the summary returns. The summary carries no
// ProcurementPortalSharedSecret (only the full preference from
// GetProcurementPortalPreference does), so nothing needs redacting. The
// summary has no name, and its two statuses (e-invoice delivery and purchase
// order retrieval) describe separate features, so neither Name nor Status is
// set.
//
// ListProcurementPortalPreferences arrived in SDK v1.9.0 (2025-11-21), almost
// a year after the service (v1.0.0, 2024-12-02), and is a preview API. On the
// first call, the shapes an unrouted operation takes are a sub-feature gap,
// skipped silently; see isInvoicingPortalPrefsNotDeployed. A first-call
// payer-only rejection is returned as not-entitled for scanInvoicing to weigh.
// Once a page has succeeded the op is deployed: a later error of any shape
// except a denial is returned, after the rows listed so far are stored.
func scanInvoicingProcurementPortalPreferences(ctx context.Context, client invoicingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "invoicing:ListProcurementPortalPreferences"
	pager := invoicing.NewListProcurementPortalPreferencesPaginator(client, &invoicing.ListProcurementPortalPreferencesInput{})
	var batch []*store.Resource
	var listErr error
	listed := false
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			switch {
			case !listed && isInvoicingPortalPrefsNotDeployed(perr):
				return 0, 0, nil
			case !listed && isPayerAccountOnly(perr):
				return 0, 0, markServiceNotEntitled(perr)
			case isAccessDenied(perr):
				_ = skipIfAccessDenied(st, op, acct.ID, region, perr)
			default:
				listErr = fmt.Errorf("%s: %w", op, perr)
			}
			break
		}
		listed = true
		for _, p := range out.ProcurementPortalPreferences {
			arn := sv(p.ProcurementPortalPreferenceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeInvoicingProcurementPortalPreference, NativeID: arn,
				Region: regionGlobal, CreatedAt: tp(p.CreateDate),
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	t, i, uerr := upsertBatch(st, batch, "invoicing procurement-portal-preferences")
	return t, i, errors.Join(listErr, uerr)
}

// isInvoicingPortalPrefsNotDeployed reports the shapes an unrouted
// ListProcurementPortalPreferences call takes on an endpoint that predates
// it: UnknownOperationException, the gateway's "Unable to determine
// service/operation name", or an HTTP 404. The op models no not-found error,
// so a 404 on its unfiltered first call means the op is absent.
func isInvoicingPortalPrefsNotDeployed(err error) bool {
	return isAPIErrorCode(err, "UnknownOperationException") || isServiceNotAvailableInRegion(err) || isHTTP404(err)
}
