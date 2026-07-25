package aws

import (
	"context"
	"fmt"

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
	registerType(restype.Descriptor{Type: TypeIoTFWFleet, Service: "iotfleetwise"})
	registerType(restype.Descriptor{Type: TypeIoTFWModelManifest, Service: "iotfleetwise"})
	registerType(restype.Descriptor{Type: TypeIoTFWSignalCatalog, Service: "iotfleetwise", Leaf: true})
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
	ListFleets(context.Context, *iotfleetwise.ListFleetsInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListFleetsOutput, error)
	ListModelManifests(context.Context, *iotfleetwise.ListModelManifestsInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListModelManifestsOutput, error)
	ListSignalCatalogs(context.Context, *iotfleetwise.ListSignalCatalogsInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListSignalCatalogsOutput, error)
	ListStateTemplates(context.Context, *iotfleetwise.ListStateTemplatesInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListStateTemplatesOutput, error)
	ListVehicles(context.Context, *iotfleetwise.ListVehiclesInput, ...func(*iotfleetwise.Options)) (*iotfleetwise.ListVehiclesOutput, error)
}

func scanIoTFleetWise(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iotfleetwise.NewFromConfig(acct.cfg, func(o *iotfleetwise.Options) { o.Region = region })

	if ferr := gateIoTFleetWise(ctx, client); ferr != nil {
		return 0, 0, ferr
	}

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIoTFWCampaigns(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFWDecoderManifests(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFWFleets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFWModelManifests(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFWSignalCatalogs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFWStateTemplates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTFWVehicles(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
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

func scanIoTFWDecoderManifests(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotfleetwise.NewListDecoderManifestsPaginator(client, &iotfleetwise.ListDecoderManifestsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isClosedToNewCustomers(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotfleetwise:ListDecoderManifests", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotfleetwise:ListDecoderManifests: %w", perr)
		}
		for _, d := range out.Summaries {
			arn := sv(d.Arn)
			if arn == "" {
				continue
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
	return upsertBatch(st, batch, "iotfleetwise decoder-manifests")
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

func scanIoTFWStateTemplates(ctx context.Context, client iotFWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotfleetwise.NewListStateTemplatesPaginator(client, &iotfleetwise.ListStateTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isIoTFleetWiseFeatureNotAuthorized(perr) {
				return 0, 0, markServiceDisabled(perr)
			}
			if isClosedToNewCustomers(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotfleetwise:ListStateTemplates", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotfleetwise:ListStateTemplates: %w", perr)
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
