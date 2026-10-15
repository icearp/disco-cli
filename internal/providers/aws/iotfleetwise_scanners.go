package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotfleetwise"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isIoTFleetWiseFeatureNotAuthorized distinguishes the per-feature "Account
// is not authorized to use this feature" gate from a real IAM denial. For
// opt-in surfaces (e.g. ListStateTemplates) that base IoTFleetWise access
// doesn't unlock.
func isIoTFleetWiseFeatureNotAuthorized(err error) bool {
	return isAccessDeniedWithMessage(err, "not authorized to use this feature")
}

func init() {
	registerType(restype.Descriptor{Type: TypeIoTFWCampaign, Service: "iotfleetwise"})
	registerType(restype.Descriptor{Type: TypeIoTFWDecoderManifest, Service: "iotfleetwise"})
	registerType(restype.Descriptor{Type: TypeIoTFWDecoderManifestNetworkInterface, Service: "iotfleetwise"})
	registerType(restype.Descriptor{Type: TypeIoTFWDecoderManifestSignal, Service: "iotfleetwise"})
	registerType(restype.Descriptor{Type: TypeIoTFWFleet, Service: "iotfleetwise"})
	registerType(restype.Descriptor{Type: TypeIoTFWModelManifest, Service: "iotfleetwise"})
	registerType(restype.Descriptor{Type: TypeIoTFWSignalCatalog, Service: "iotfleetwise"})
	registerType(restype.Descriptor{Type: TypeIoTFWStateTemplate, Service: "iotfleetwise"})
	registerType(restype.Descriptor{Type: TypeIoTFWVehicle, Service: "iotfleetwise"})
	registerService(serviceEntry{
		name: "aws:iotfleetwise",
		fn:   scanIoTFleetWise,
	})
}

type iotFWAPI interface {
	ListCampaigns(context.Context, *iotfleetwise.ListCampaignsInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListCampaignsOutput, error)
	ListDecoderManifests(context.Context, *iotfleetwise.ListDecoderManifestsInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListDecoderManifestsOutput, error)
	ListDecoderManifestNetworkInterfaces(context.Context, *iotfleetwise.ListDecoderManifestNetworkInterfacesInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListDecoderManifestNetworkInterfacesOutput, error)
	ListDecoderManifestSignals(context.Context, *iotfleetwise.ListDecoderManifestSignalsInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListDecoderManifestSignalsOutput, error)
	ListFleets(context.Context, *iotfleetwise.ListFleetsInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListFleetsOutput, error)
	ListModelManifests(context.Context, *iotfleetwise.ListModelManifestsInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListModelManifestsOutput, error)
	ListSignalCatalogs(context.Context, *iotfleetwise.ListSignalCatalogsInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListSignalCatalogsOutput, error)
	ListStateTemplates(context.Context, *iotfleetwise.ListStateTemplatesInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListStateTemplatesOutput, error)
	ListVehicles(context.Context, *iotfleetwise.ListVehiclesInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListVehiclesOutput, error)
}

const (
	iotFWListDecoderManifestNetworkInterfacesOp = "iotfleetwise:ListDecoderManifestNetworkInterfaces"
	iotFWListDecoderManifestSignalsOp           = "iotfleetwise:ListDecoderManifestSignals"
	iotFWListStateTemplatesOp                   = "iotfleetwise:ListStateTemplates"
)

// iotFWPageLimit is the MaxResults cap the IoT FleetWise model documents for
// the decoder manifest child list ops.
const iotFWPageLimit = 100

// NativeID kind segments of decoder manifest children, which have no ARN.
const (
	iotFWNetworkInterfaceKind = "network-interface"
	iotFWSignalKind           = "signal"
)

func scanIoTFleetWise(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := iotfleetwise.NewFromConfig(acct.cfg, func(o *iotfleetwise.Options) { o.Region = region })
	return scanIoTFleetWiseAll(ctx, client, acct, region, st, scanID)
}

// scanIoTFleetWiseAll runs the older phases, which stop at their first hard
// error, then the decoder manifest child phases over the manifests the older
// phases stored (none if they failed before the manifest phase). A child phase
// failure costs no other phase its rows, and an older phase failing after the
// manifest phase does not skip the child phases. The
// older phases' transient error is demoted to a warning: one transient member
// would make the dispatcher read the whole join as transient, hiding a child
// phase's hard error. The child phases tally their transient errors per
// manifest, so none of theirs is transient.
//
// The state-template feature-gate denial becomes markServiceDisabled only when
// no phase stored a row and none failed otherwise. Else it becomes one
// warning, because the dispatcher discards a sentinel's counts and ranks it
// above a hard error, which would hide both the rows and another phase's
// failure.
func scanIoTFleetWiseAll(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if ferr := gateIoTFleetWise(ctx, client); ferr != nil {
		return 0, 0, ferr
	}
	total, inserted, manifests, stateTemplatesGated, err := scanIoTFWOlderPhases(ctx, client, acct, region, st, scanID)
	ts, is, serr := scanIoTFWDecoderManifestSignals(ctx, client, acct, region, st, scanID, manifests)
	tn, in, nerr := scanIoTFWDecoderManifestNetworkInterfaces(ctx, client, acct, region, st, scanID, manifests)
	total, inserted = total+ts+tn, inserted+is+in
	err = errors.Join(demoteTransient(st, "iotfleetwise", acct, region, err), serr, nerr)
	if stateTemplatesGated != nil {
		if total == 0 && err == nil {
			return 0, 0, markServiceDisabled(stateTemplatesGated)
		}
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: iotFWListStateTemplatesOp, Scope: acct.ID + "/" + region,
			Message: "state templates not listed: " + stateTemplatesGated.Error(),
		})
	}
	return total, inserted, err
}

// scanIoTFWOlderPhases runs the phases that predate the decoder manifest
// children, stopping at the first hard error. It also returns the decoder
// manifests stored, and the state-template phase's feature-gate denial, which
// does not stop the phases after it.
func scanIoTFWOlderPhases(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, manifests []childParent, stateTemplatesGated, err error) {
	total, inserted, err = scanIoTFWCampaigns(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, nil, nil, err
	}
	t, i, manifests, err := scanIoTFWDecoderManifests(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, nil, nil, err
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIoTFWFleets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFWModelManifests(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFWSignalCatalogs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFWStateTemplates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFWVehicles(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if isIoTFleetWiseFeatureNotAuthorized(perr) {
			stateTemplatesGated = perr
			continue
		}
		if perr != nil {
			return total, inserted, manifests, stateTemplatesGated, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, manifests, stateTemplatesGated, nil
}

// gateIoTFleetWise probes the cheapest list op once. An empty-message
// AccessDeniedException (closed-to-new-customers) means the account can't
// self-enable the service — return markServiceNotEntitled so the dispatcher
// renders `(not available to this account)` once instead of N per-phase
// warnings. Any other error or success returns nil and the phase loop runs.
func gateIoTFleetWise(ctx context.Context, client iotFWAPI) error {
	mr := int32(1)
	_, err := client.ListCampaigns(ctx, &iotfleetwise.ListCampaignsInput{MaxResults: &mr})
	if err != nil && isClosedToNewCustomers(err) {
		return markServiceNotEntitled(err)
	}
	return nil
}

func scanIoTFWCampaigns(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotfleetwise.NewListCampaignsPaginator(client, &iotfleetwise.ListCampaignsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isClosedToNewCustomers(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotfleetwise:ListCampaigns", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotfleetwise:ListCampaigns: %w", perr)
		}
		for _, c := range out.CampaignSummaries {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTFWCampaign, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotfleetwise campaigns")
}

// scanIoTFWDecoderManifests stores the region's decoder manifests and returns
// those with a name, the key the child list ops take, as their parents.
func scanIoTFWDecoderManifests(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	pager := iotfleetwise.NewListDecoderManifestsPaginator(client, &iotfleetwise.ListDecoderManifestsInput{})
	var (
		batch     []*store.Resource
		manifests []childParent
	)
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isClosedToNewCustomers(perr) {
				return 0, 0, nil, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotfleetwise:ListDecoderManifests", acct.ID, region, perr)
				return 0, 0, nil, nil
			}
			return 0, 0, nil, fmt.Errorf("iotfleetwise:ListDecoderManifests: %w", perr)
		}
		for _, d := range out.Summaries {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			if name := sv(d.Name); name != "" {
				manifests = append(manifests, childParent{id: name, arn: arn})
			}
			label := sv(d.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTFWDecoderManifest, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "iotfleetwise decoder-manifests")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, manifests, nil
}

func scanIoTFWFleets(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotfleetwise.NewListFleetsPaginator(client, &iotfleetwise.ListFleetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isClosedToNewCustomers(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotfleetwise:ListFleets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotfleetwise:ListFleets: %w", perr)
		}
		for _, f := range out.FleetSummaries {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			label := sv(f.Id)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTFWFleet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotfleetwise fleets")
}

func scanIoTFWModelManifests(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotfleetwise.NewListModelManifestsPaginator(client, &iotfleetwise.ListModelManifestsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isClosedToNewCustomers(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotfleetwise:ListModelManifests", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotfleetwise:ListModelManifests: %w", perr)
		}
		for _, m := range out.Summaries {
			arn := sv(m.Arn)
			if arn == "" {
				continue
			}
			label := sv(m.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTFWModelManifest, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotfleetwise model-manifests")
}

func scanIoTFWSignalCatalogs(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotfleetwise.NewListSignalCatalogsPaginator(client, &iotfleetwise.ListSignalCatalogsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isClosedToNewCustomers(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotfleetwise:ListSignalCatalogs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotfleetwise:ListSignalCatalogs: %w", perr)
		}
		for _, s := range out.Summaries {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTFWSignalCatalog, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotfleetwise signal-catalogs")
}

// scanIoTFWStateTemplates stores the region's state templates. The feature-gate
// denial comes back unwrapped, for scanIoTFleetWiseAll to weigh against the
// rows the other phases stored.
func scanIoTFWStateTemplates(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotfleetwise.NewListStateTemplatesPaginator(client, &iotfleetwise.ListStateTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isIoTFleetWiseFeatureNotAuthorized(perr) {
				return 0, 0, perr
			}
			if isClosedToNewCustomers(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, iotFWListStateTemplatesOp, acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("%s: %w", iotFWListStateTemplatesOp, perr)
		}
		for _, s := range out.Summaries {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = sv(s.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTFWStateTemplate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotfleetwise state-templates")
}

func scanIoTFWVehicles(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotfleetwise.NewListVehiclesPaginator(client, &iotfleetwise.ListVehiclesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isClosedToNewCustomers(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotfleetwise:ListVehicles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotfleetwise:ListVehicles: %w", perr)
		}
		for _, v := range out.VehicleSummaries {
			arn := sv(v.Arn)
			if arn == "" {
				continue
			}
			label := sv(v.VehicleName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTFWVehicle, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotfleetwise vehicles")
}

// iotFWDecoderManifestChildNativeID keys a decoder manifest child under its
// manifest: signal decoders and network interfaces have no ARN (the Service
// Reference defines only the manifest ARN). CreateDecoderManifest requires
// signal decoder names and network interface ids to be unique within the
// manifest, and neither can contain "/" (model patterns ^[a-zA-Z0-9_.]+$ and
// ^[-a-zA-Z0-9_.]+$).
func iotFWDecoderManifestChildNativeID(manifestARN, kind, key string) string {
	return manifestARN + "/" + kind + "/" + key
}

// isIoTFWManifestChildSkip matches the child-list errors skipped silently for
// one manifest: ResourceNotFoundException (the manifest was deleted after it
// was listed) and the closed-to-new-customers denial every phase here skips.
func isIoTFWManifestChildSkip(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException") || isClosedToNewCustomers(err)
}

// iotFWManifestChildFanOut runs storeChildren for each decoder manifest
// concurrently (childFanOutStored), so a manifest's signal decoders, which can
// run to thousands, are written a page at a time rather than held for one
// upsert. A manifest whose call still fails transiently after the SDK's
// retries is skipped, keeping its siblings' rows, and the skips are reported
// in one warning. Rows a manifest stored before its error stay and are
// counted.
//
// A manifest refused with ValidationException is skipped the same way, with
// one warning for all such manifests. The model documents no error for a
// manifest in a particular status, and how these ops answer for a VALIDATING
// or INVALID manifest has not been observed; if it is ValidationException,
// failing the phase would fail it on every scan and cancel the sibling
// manifests, while skipping silently would hide a real input error.
func iotFWManifestChildFanOut(ctx context.Context, st *store.Store, acct *account, region, op string, manifests []childParent, storeChildren func(context.Context, childParent) (int, int, error)) (int, int, error) {
	var (
		transient   transientTally
		notListable iotFWNotListableTally
	)
	total, inserted, err := childFanOutStored(ctx, st, acct, region, op, manifests, isIoTFWManifestChildSkip,
		func(ctx context.Context, m childParent) (int, int, error) {
			t, i, err := storeChildren(ctx, m)
			switch {
			case isTransientNetworkError(err):
				transient.skipped(m.arn, err)
				return t, i, nil
			case isAPIErrorCode(err, "ValidationException"):
				notListable.skipped(m.arn, err)
				return t, i, nil
			}
			return t, i, err
		})
	transient.warnSkipped(st, op, acct, region)
	notListable.warnSkipped(st, op, acct, region)
	return total, inserted, err
}

// iotFWNotListableTally collects the manifests whose child listing IoT
// FleetWise rejected with ValidationException, so that one warning covers
// them all.
type iotFWNotListableTally struct {
	mu    sync.Mutex
	count int
	first string
	err   error
}

func (t *iotFWNotListableTally) skipped(manifest string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.count == 0 {
		t.first, t.err = manifest, err
	}
	t.count++
}

func (t *iotFWNotListableTally) warnSkipped(st *store.Store, op string, acct *account, region string) {
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d decoder manifest(s) skipped with ValidationException; the manifest may not be in a listable state, first %s: %v", t.count, t.first, t.err),
	})
}

// upsertIoTFWManifestChildren stores one page of a manifest's children and
// links each under the manifest.
func upsertIoTFWManifestChildren(st *store.Store, acct *account, op string, manifest childParent, rows []*store.Resource) (int, int, error) {
	parentID := store.ResourceID("aws", acct.ID, manifest.arn)
	children := make([]containedChild, len(rows))
	for i, r := range rows {
		children[i] = containedChild{row: r, parentID: parentID}
	}
	return upsertContained(st, op, children)
}

// scanIoTFWDecoderManifestSignals stores each decoder manifest's signal
// decoders, keyed by fully qualified name.
func scanIoTFWDecoderManifestSignals(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string, manifests []childParent) (int, int, error) {
	op := iotFWListDecoderManifestSignalsOp
	return iotFWManifestChildFanOut(ctx, st, acct, region, op, manifests, func(ctx context.Context, m childParent) (int, int, error) {
		pager := iotfleetwise.NewListDecoderManifestSignalsPaginator(client,
			&iotfleetwise.ListDecoderManifestSignalsInput{Name: sdkaws.String(m.id)},
			func(o *iotfleetwise.ListDecoderManifestSignalsPaginatorOptions) { o.Limit = iotFWPageLimit })
		var total, inserted int
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return total, inserted, err
			}
			var rows []*store.Resource
			for _, s := range out.SignalDecoders {
				name := sv(s.FullyQualifiedName)
				if name == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeIoTFWDecoderManifestSignal, NativeID: iotFWDecoderManifestChildNativeID(m.arn, iotFWSignalKind, name),
					Name: &name, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
			}
			t, i, err := upsertIoTFWManifestChildren(st, acct, op, m, rows)
			if err != nil {
				return total, inserted, err
			}
			total += t
			inserted += i
		}
		return total, inserted, nil
	})
}

// scanIoTFWDecoderManifestNetworkInterfaces stores each decoder manifest's
// network interfaces, keyed by interface id.
func scanIoTFWDecoderManifestNetworkInterfaces(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string, manifests []childParent) (int, int, error) {
	op := iotFWListDecoderManifestNetworkInterfacesOp
	return iotFWManifestChildFanOut(ctx, st, acct, region, op, manifests, func(ctx context.Context, m childParent) (int, int, error) {
		pager := iotfleetwise.NewListDecoderManifestNetworkInterfacesPaginator(client,
			&iotfleetwise.ListDecoderManifestNetworkInterfacesInput{Name: sdkaws.String(m.id)},
			func(o *iotfleetwise.ListDecoderManifestNetworkInterfacesPaginatorOptions) { o.Limit = iotFWPageLimit })
		var total, inserted int
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return total, inserted, err
			}
			var rows []*store.Resource
			for _, ni := range out.NetworkInterfaces {
				id := sv(ni.InterfaceId)
				if id == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeIoTFWDecoderManifestNetworkInterface, NativeID: iotFWDecoderManifestChildNativeID(m.arn, iotFWNetworkInterfaceKind, id),
					Name: &id, Region: &region, AttributesJSON: mustJSON(ni), DiscoveredBy: scanID,
				})
			}
			t, i, err := upsertIoTFWManifestChildren(st, acct, op, m, rows)
			if err != nil {
				return total, inserted, err
			}
			total += t
			inserted += i
		}
		return total, inserted, nil
	})
}
