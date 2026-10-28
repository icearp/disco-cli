package aws

import (
	"cmp"
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iotfleetwise"
	iotfwtypes "github.com/aws/aws-sdk-go-v2/service/iotfleetwise/types"
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
	ListTagsForResource(context.Context, *iotfleetwise.ListTagsForResourceInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListTagsForResourceOutput, error)
}

const (
	iotFWListCampaignsOp                        = "iotfleetwise:ListCampaigns"
	iotFWListDecoderManifestsOp                 = "iotfleetwise:ListDecoderManifests"
	iotFWListDecoderManifestNetworkInterfacesOp = "iotfleetwise:ListDecoderManifestNetworkInterfaces"
	iotFWListDecoderManifestSignalsOp           = "iotfleetwise:ListDecoderManifestSignals"
	iotFWListFleetsOp                           = "iotfleetwise:ListFleets"
	iotFWListModelManifestsOp                   = "iotfleetwise:ListModelManifests"
	iotFWListSignalCatalogsOp                   = "iotfleetwise:ListSignalCatalogs"
	iotFWListStateTemplatesOp                   = "iotfleetwise:ListStateTemplates"
	iotFWListVehiclesOp                         = "iotfleetwise:ListVehicles"
	iotFWListTagsOp                             = "iotfleetwise:ListTagsForResource"
)

// iotFWPageLimit is the MaxResults cap the IoT FleetWise model documents for
// every list op here.
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

// iotFWTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the ARN its listing returned. The op does not page.
// The Service Reference lists every type read here as one ListTagsForResource
// takes; the decoder manifest children have no ARN, so they are not read.
func iotFWTags(client iotFWAPI) rowTagReader {
	return rowTagReader{
		op:       iotFWListTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &iotfleetwise.ListTagsForResourceInput{ResourceARN: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t iotfwtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanIoTFleetWiseAll runs every listing as its own phase, one at a time
// (runHostPhases): a phase's error does not stop the later ones; a failed
// write and ctx's end do. The decoder manifest child phases run last, over
// the manifests the manifest phase stored. A listing whose first page is
// refused as closed to new customers is read as empty, silently
// (firstPageGapPages): closure can differ per op on one account
// (gateIoTFleetWise probes only ListCampaigns), and no IAM change lifts it. Every other account-wide listing
// is written a page at a time with its tags (storeTokenPagesTags), since a
// region can hold many vehicles; the manifests are held whole, as the child
// phases' parents.
//
// The state-template feature-gate denial becomes markServiceDisabled only when
// no phase stored a row and none failed otherwise. Else it becomes one
// warning, because the dispatcher discards a sentinel's counts and ranks it
// above a hard error, which would hide both the rows and another phase's
// failure. After a failed write it sends nothing.
func scanIoTFleetWiseAll(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if ferr := gateIoTFleetWise(ctx, client); ferr != nil {
		return 0, 0, ferr
	}
	tags := iotFWTags(client)
	var manifests []childParent
	gated := listDenial{isDenied: isIoTFleetWiseFeatureNotAuthorized, unread: "state templates on later pages"}
	total, inserted, err := runHostPhases(ctx, st, acct, region, "iotfleetwise", 0, []hostPhase{
		{op: iotFWListCampaignsOp, run: func(p *phaseTotals) bool {
			return scanIoTFWCampaigns(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: iotFWListDecoderManifestsOp, run: func(p *phaseTotals) (failed bool) {
			manifests, failed = scanIoTFWDecoderManifests(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: iotFWListFleetsOp, run: func(p *phaseTotals) bool {
			return scanIoTFWFleets(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: iotFWListModelManifestsOp, run: func(p *phaseTotals) bool {
			return scanIoTFWModelManifests(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: iotFWListSignalCatalogsOp, run: func(p *phaseTotals) bool {
			return scanIoTFWSignalCatalogs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: iotFWListStateTemplatesOp, run: func(p *phaseTotals) bool {
			return scanIoTFWStateTemplates(ctx, client, acct, region, st, scanID, &gated, tags, p)
		}},
		{op: iotFWListVehiclesOp, run: func(p *phaseTotals) bool {
			return scanIoTFWVehicles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, iotFWListDecoderManifestSignalsOp, func() (int, int, error) {
			return scanIoTFWDecoderManifestSignals(ctx, client, acct, region, st, scanID, manifests)
		}),
		countedPhase(0, iotFWListDecoderManifestNetworkInterfacesOp, func() (int, int, error) {
			return scanIoTFWDecoderManifestNetworkInterfaces(ctx, client, acct, region, st, scanID, manifests)
		}),
	})
	if gated.err != nil && !errors.Is(err, store.ErrStoreWrite) {
		if total == 0 && err == nil {
			return 0, 0, markServiceDisabled(gated.err)
		}
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: iotFWListStateTemplatesOp, Scope: acct.ID + "/" + region,
			Message: "state templates not listed: " + gated.err.Error(),
		})
	}
	return total, inserted, err
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

// iotFWRow is the row of a listed element keyed by its ARN and named label.
// An element without an ARN is not stored; what names the elements in the
// warning.
func iotFWRow(acct *account, region, scanID, typ, what, arn, label string, item any) (*store.Resource, string, string) {
	if arn == "" {
		return nil, what + " have no ARN", cmp.Or(label, "(an element with no identifier)")
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

func scanIoTFWCampaigns(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, iotFWListCampaignsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]iotfwtypes.CampaignSummary, *string, error) {
			out, err := client.ListCampaigns(ctx, &iotfleetwise.ListCampaignsInput{MaxResults: sdkaws.Int32(iotFWPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.CampaignSummaries, out.NextToken, nil
		}),
		func(it iotfwtypes.CampaignSummary) (*store.Resource, string, string) {
			arn := sv(it.Arn)
			return iotFWRow(acct, region, scanID, TypeIoTFWCampaign, "campaigns", arn, cmp.Or(sv(it.Name), arn), it)
		}, tags, p)
}

// scanIoTFWDecoderManifests stores the region's decoder manifests with their
// tags and returns the stored ones that have a name, the key the child list
// ops take, as their parents.
func scanIoTFWDecoderManifests(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, iotFWListDecoderManifestsOp, "decoder manifests", "Name",
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]iotfwtypes.DecoderManifestSummary, *string, error) {
			out, err := client.ListDecoderManifests(ctx, &iotfleetwise.ListDecoderManifestsInput{MaxResults: sdkaws.Int32(iotFWPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Summaries, out.NextToken, nil
		}),
		func(it iotfwtypes.DecoderManifestSummary) (*store.Resource, string, string) {
			arn := sv(it.Arn)
			return iotFWRow(acct, region, scanID, TypeIoTFWDecoderManifest, "decoder manifests", arn, cmp.Or(sv(it.Name), arn), it)
		},
		func(it iotfwtypes.DecoderManifestSummary) string { return sv(it.Name) },
		tags, p)
}

func scanIoTFWFleets(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, iotFWListFleetsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]iotfwtypes.FleetSummary, *string, error) {
			out, err := client.ListFleets(ctx, &iotfleetwise.ListFleetsInput{MaxResults: sdkaws.Int32(iotFWPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.FleetSummaries, out.NextToken, nil
		}),
		func(it iotfwtypes.FleetSummary) (*store.Resource, string, string) {
			arn := sv(it.Arn)
			return iotFWRow(acct, region, scanID, TypeIoTFWFleet, "fleets", arn, cmp.Or(sv(it.Id), arn), it)
		}, tags, p)
}

func scanIoTFWModelManifests(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, iotFWListModelManifestsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]iotfwtypes.ModelManifestSummary, *string, error) {
			out, err := client.ListModelManifests(ctx, &iotfleetwise.ListModelManifestsInput{MaxResults: sdkaws.Int32(iotFWPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Summaries, out.NextToken, nil
		}),
		func(it iotfwtypes.ModelManifestSummary) (*store.Resource, string, string) {
			arn := sv(it.Arn)
			return iotFWRow(acct, region, scanID, TypeIoTFWModelManifest, "model manifests", arn, cmp.Or(sv(it.Name), arn), it)
		}, tags, p)
}

func scanIoTFWSignalCatalogs(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, iotFWListSignalCatalogsOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]iotfwtypes.SignalCatalogSummary, *string, error) {
			out, err := client.ListSignalCatalogs(ctx, &iotfleetwise.ListSignalCatalogsInput{MaxResults: sdkaws.Int32(iotFWPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Summaries, out.NextToken, nil
		}),
		func(it iotfwtypes.SignalCatalogSummary) (*store.Resource, string, string) {
			arn := sv(it.Arn)
			return iotFWRow(acct, region, scanID, TypeIoTFWSignalCatalog, "signal catalogs", arn, cmp.Or(sv(it.Name), arn), it)
		}, tags, p)
}

// scanIoTFWStateTemplates stores the region's state templates with their
// tags. The feature-gate denial ends the listing without an error and is
// left in gated, for scanIoTFleetWiseAll to weigh against the rows the other
// phases stored. A state template is named by its Name, else its Id.
func scanIoTFWStateTemplates(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string, gated *listDenial, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, iotFWListStateTemplatesOp,
		deniedPages(gated, firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]iotfwtypes.StateTemplateSummary, *string, error) {
			out, err := client.ListStateTemplates(ctx, &iotfleetwise.ListStateTemplatesInput{MaxResults: sdkaws.Int32(iotFWPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Summaries, out.NextToken, nil
		})),
		func(it iotfwtypes.StateTemplateSummary) (*store.Resource, string, string) {
			return iotFWRow(acct, region, scanID, TypeIoTFWStateTemplate, "state templates", sv(it.Arn), cmp.Or(sv(it.Name), sv(it.Id)), it)
		}, tags, p)
}

func scanIoTFWVehicles(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, iotFWListVehiclesOp,
		firstPageGapPages(isClosedToNewCustomers, func(ctx context.Context, token *string) ([]iotfwtypes.VehicleSummary, *string, error) {
			out, err := client.ListVehicles(ctx, &iotfleetwise.ListVehiclesInput{MaxResults: sdkaws.Int32(iotFWPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VehicleSummaries, out.NextToken, nil
		}),
		func(it iotfwtypes.VehicleSummary) (*store.Resource, string, string) {
			arn := sv(it.Arn)
			return iotFWRow(acct, region, scanID, TypeIoTFWVehicle, "vehicles", arn, cmp.Or(sv(it.VehicleName), arn), it)
		}, tags, p)
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
// concurrently (childFanOutStoredSkipNotReady), so a manifest's signal
// decoders, which can run to thousands, are written a page at a time rather
// than held for one upsert. A manifest whose call still fails transiently after the SDK's
// retries is skipped, keeping its siblings' rows, and the skips are reported
// in one warning. Rows a manifest stored before its error stay and are
// counted. A repeated NextToken ends a manifest's listing with
// errRepeatedNextToken (eachTokenPage), which fails the phase.
//
// A manifest refused with ValidationException is skipped the same way, with
// one warning for all such manifests. The model documents no error for a
// manifest in a particular status, and how these ops answer for a VALIDATING
// or INVALID manifest has not been observed; if it is ValidationException,
// failing the phase would fail it on every scan and cancel the sibling
// manifests, while skipping silently would hide a real input error.
func iotFWManifestChildFanOut(ctx context.Context, st *store.Store, acct *account, region, op string, manifests []childParent, storeChildren func(context.Context, childParent) (int, int, error)) (int, int, error) {
	return childFanOutStoredSkipNotReady(ctx, st, acct, region, op, manifests, isIoTFWManifestChildSkip, byParentARN,
		"decoder manifest(s)", "the manifest may not be in a listable state", []string{"ValidationException"}, storeChildren)
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
		var total, inserted int
		err := eachTokenPage(ctx, func(ctx context.Context, token *string) ([]iotfwtypes.SignalDecoder, *string, error) {
			out, err := client.ListDecoderManifestSignals(ctx, &iotfleetwise.ListDecoderManifestSignalsInput{
				Name: sdkaws.String(m.id), MaxResults: sdkaws.Int32(iotFWPageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.SignalDecoders, out.NextToken, nil
		}, func(signals []iotfwtypes.SignalDecoder) error {
			var rows []*store.Resource
			for _, s := range signals {
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
			total += t
			inserted += i
			return err
		})
		return total, inserted, err
	})
}

// scanIoTFWDecoderManifestNetworkInterfaces stores each decoder manifest's
// network interfaces, keyed by interface id.
func scanIoTFWDecoderManifestNetworkInterfaces(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string, manifests []childParent) (int, int, error) {
	op := iotFWListDecoderManifestNetworkInterfacesOp
	return iotFWManifestChildFanOut(ctx, st, acct, region, op, manifests, func(ctx context.Context, m childParent) (int, int, error) {
		var total, inserted int
		err := eachTokenPage(ctx, func(ctx context.Context, token *string) ([]iotfwtypes.NetworkInterface, *string, error) {
			out, err := client.ListDecoderManifestNetworkInterfaces(ctx, &iotfleetwise.ListDecoderManifestNetworkInterfacesInput{
				Name: sdkaws.String(m.id), MaxResults: sdkaws.Int32(iotFWPageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.NetworkInterfaces, out.NextToken, nil
		}, func(ifaces []iotfwtypes.NetworkInterface) error {
			var rows []*store.Resource
			for _, ni := range ifaces {
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
			total += t
			inserted += i
			return err
		})
		return total, inserted, err
	})
}
