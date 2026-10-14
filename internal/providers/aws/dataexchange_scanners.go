package aws

import (
	"context"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dataexchange"
	dxtypes "github.com/aws/aws-sdk-go-v2/service/dataexchange/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDataExchangeDataSets, Service: "dataexchange"})
	registerType(restype.Descriptor{Type: TypeDataExchangeDataGrants, Service: "dataexchange"})
	registerType(restype.Descriptor{Type: TypeDataExchangeEventActions, Service: "dataexchange"})
	registerType(restype.Descriptor{Type: TypeDataExchangeDataSetRevision, Service: "dataexchange"})
	// An API asset's ApiKey is a credential, and its specification download
	// URL is a time-limited link that grants whoever holds it the
	// specification.
	registerType(restype.Descriptor{
		Type: TypeDataExchangeRevisionAsset, Service: "dataexchange",
		Redact: []redact.Rule{
			{Path: "AssetDetails.ApiGatewayApiAsset.ApiKey", Mode: redact.RedactScalar},
			{Path: "AssetDetails.ApiGatewayApiAsset.ApiSpecificationDownloadUrl", Mode: redact.RedactScalar},
		},
	})
	registerType(restype.Descriptor{Type: TypeDataExchangeReceivedDataGrant, Service: "dataexchange"})
	registerService(serviceEntry{
		name: "aws:dataexchange",
		fn:   scanDataExchange,
	})
}

type dataExchangeAPI interface {
	ListDataSets(context.Context, *dataexchange.ListDataSetsInput, ...func(*dataexchange.Options)) (*dataexchange.ListDataSetsOutput, error)
	ListDataGrants(context.Context, *dataexchange.ListDataGrantsInput, ...func(*dataexchange.Options)) (*dataexchange.ListDataGrantsOutput, error)
	ListEventActions(context.Context, *dataexchange.ListEventActionsInput, ...func(*dataexchange.Options)) (*dataexchange.ListEventActionsOutput, error)
	ListDataSetRevisions(context.Context, *dataexchange.ListDataSetRevisionsInput, ...func(*dataexchange.Options)) (*dataexchange.ListDataSetRevisionsOutput, error)
	ListRevisionAssets(context.Context, *dataexchange.ListRevisionAssetsInput, ...func(*dataexchange.Options)) (*dataexchange.ListRevisionAssetsOutput, error)
	ListReceivedDataGrants(context.Context, *dataexchange.ListReceivedDataGrantsInput, ...func(*dataexchange.Options)) (*dataexchange.ListReceivedDataGrantsOutput, error)
}

// dxDataSet is a data set stored this scan, as a revision fan-out parent.
type dxDataSet struct {
	childParent
	assetType dxtypes.AssetType
}

// dxRevision is a revision stored this scan, as an asset fan-out parent;
// ListRevisionAssets keys on the revision's data set as well as the revision.
// childParent.id is "{dataSetID}/{revisionID}" so a failed call names both;
// the call itself reads the two fields.
type dxRevision struct {
	childParent
	dataSetID, revisionID string
}

// scanDataExchange discovers owned data sets with their revisions and share
// assets, data grants shared from and to this account, and event actions
// automating revision exports. Not scanned: S3 snapshot assets (data-set
// content, see scanDataExchangeAssets), entitled-* (consumer-side marketplace
// subscriptions), jobs (import/export run records).
//
// The received-grant, revision and asset phases run after every older phase,
// so their failure cannot cost the older rows; received grants go first among
// them because they depend on nothing and the two fan-outs are unbounded.
func scanDataExchange(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := dataexchange.NewFromConfig(acct.cfg, func(o *dataexchange.Options) { o.Region = region })

	var (
		dataSets  []dxDataSet
		revisions []dxRevision
	)
	for _, phase := range []func() (int, int, error){
		func() (t, i int, err error) {
			dataSets, t, i, err = scanDataExchangeDataSets(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanDataExchangeDataGrants(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDataExchangeEventActions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanDataExchangeReceivedDataGrants(ctx, client, acct, region, st, scanID)
		},
		func() (t, i int, err error) {
			revisions, t, i, err = scanDataExchangeRevisions(ctx, client, acct, region, st, scanID, dataSets)
			return t, i, err
		},
		func() (int, int, error) {
			return scanDataExchangeAssets(ctx, client, acct, region, st, scanID, revisions)
		},
	} {
		t, i, ferr := phase()
		total += t
		inserted += i
		if ferr != nil {
			return total, inserted, ferr
		}
	}
	return total, inserted, nil
}

func scanDataExchangeDataSets(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string) ([]dxDataSet, int, int, error) {
	var (
		batch   []*store.Resource
		parents []dxDataSet
	)
	// Owned-only: entitled (subscribed-from-others) data sets are catalogued
	// separately upstream as entitled-data-sets and not scanned here.
	pager := dataexchange.NewListDataSetsPaginator(client, &dataexchange.ListDataSetsInput{Origin: sdkaws.String("OWNED")})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, 0, 0, skipIfAccessDenied(st, "dataexchange:ListDataSets", acct.ID, region, perr)
			}
			return nil, 0, 0, fmt.Errorf("dataexchange:ListDataSets: %w", perr)
		}
		for _, d := range out.DataSets {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			name := sv(d.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataExchangeDataSets, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
			if id := sv(d.Id); id != "" {
				parents = append(parents, dxDataSet{childParent: childParent{id: id, arn: arn}, assetType: d.AssetType})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "dataexchange data-sets")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, t, i, nil
}

func scanDataExchangeDataGrants(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := dataexchange.NewListDataGrantsPaginator(client, &dataexchange.ListDataGrantsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "dataexchange:ListDataGrants", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("dataexchange:ListDataGrants: %w", perr)
		}
		for _, g := range out.DataGrantSummaries {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			name := sv(g.Name)
			status := string(g.AcceptanceState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataExchangeDataGrants, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dataexchange data-grants")
}

func scanDataExchangeEventActions(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := dataexchange.NewListEventActionsPaginator(client, &dataexchange.ListEventActionsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "dataexchange:ListEventActions", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("dataexchange:ListEventActions: %w", perr)
		}
		for _, e := range out.EventActions {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			id := sv(e.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataExchangeEventActions, NativeID: arn,
				Name: &id, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dataexchange event-actions")
}

// scanDataExchangeRevisions lists the revisions of each data set stored this
// scan, storing each page as it arrives because a data set gains a revision
// per publication. It returns the stored revisions whose assets
// scanDataExchangeAssets lists: those of data sets whose AssetType is not
// S3_SNAPSHOT. A data set's AssetType is fixed at CreateDataSet and names the
// type of asset added to it.
func scanDataExchangeRevisions(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string, dataSets []dxDataSet) ([]dxRevision, int, int, error) {
	const op = "dataexchange:ListDataSetRevisions"
	parents := make([]childParent, len(dataSets))
	listsAssets := make(map[string]bool, len(dataSets))
	for i, d := range dataSets {
		parents[i] = d.childParent
		listsAssets[d.id] = d.assetType != dxtypes.AssetTypeS3Snapshot
	}
	var (
		mu        sync.Mutex
		revisions []dxRevision
	)
	total, inserted, err := childFanOutStored(ctx, st, acct, region, op, parents, isDataExchangeNotFound,
		func(ctx context.Context, p childParent) (int, int, error) {
			var total, inserted int
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			pager := dataexchange.NewListDataSetRevisionsPaginator(client, &dataexchange.ListDataSetRevisionsInput{DataSetId: &p.id})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return total, inserted, err
				}
				var (
					page    []containedChild
					pageRev []dxRevision
				)
				for _, r := range out.Revisions {
					arn, id := sv(r.Arn), sv(r.Id)
					if arn == "" {
						continue
					}
					page = append(page, containedChild{parentID: parentID, row: &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataExchangeDataSetRevision, NativeID: arn,
						Name: &id, Status: sp(dxRevisionStatus(r)), Region: &region, CreatedAt: tp(r.CreatedAt),
						AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
					}})
					if id != "" {
						pageRev = append(pageRev, dxRevision{
							childParent: childParent{id: p.id + "/" + id, arn: arn},
							dataSetID:   p.id, revisionID: id,
						})
					}
				}
				t, i, err := upsertContained(st, op, page)
				if err != nil {
					return total, inserted, err
				}
				total += t
				inserted += i
				if listsAssets[p.id] {
					mu.Lock()
					revisions = append(revisions, pageRev...)
					mu.Unlock()
				}
			}
			return total, inserted, nil
		})
	return revisions, total, inserted, err
}

// scanDataExchangeAssets lists the assets of each given revision, storing each
// page as it arrives. An asset is stored only when it is a means of fulfilling
// data — a Redshift datashare, API Gateway API, Lake Formation data permission
// or S3 data access — which points at infrastructure the provider runs and
// grants subscribers access to it. An S3 data access asset's Keys and
// KeyPrefixes are kept: they are the access the provider chose to share, not
// object contents. An S3_SNAPSHOT asset is a copy of one S3 object: the data
// itself, one row per file, so scanDataExchangeRevisions does not hand over
// the revisions of S3_SNAPSHOT data sets.
func scanDataExchangeAssets(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string, revisions []dxRevision) (int, int, error) {
	const op = "dataexchange:ListRevisionAssets"
	parents := make([]childParent, len(revisions))
	byARN := make(map[string]dxRevision, len(revisions))
	for i, r := range revisions {
		parents[i] = r.childParent
		byARN[r.arn] = r
	}
	return childFanOutStored(ctx, st, acct, region, op, parents, isDataExchangeNotFound,
		func(ctx context.Context, p childParent) (int, int, error) {
			var total, inserted int
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			rev := byARN[p.arn]
			pager := dataexchange.NewListRevisionAssetsPaginator(client, &dataexchange.ListRevisionAssetsInput{
				DataSetId: &rev.dataSetID, RevisionId: &rev.revisionID,
			})
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return total, inserted, err
				}
				var page []containedChild
				for _, a := range out.Assets {
					arn := sv(a.Arn)
					if arn == "" {
						continue
					}
					name := sv(a.Name)
					page = append(page, containedChild{parentID: parentID, row: &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeDataExchangeRevisionAsset, NativeID: arn,
						Name: &name, Region: &region, CreatedAt: tp(a.CreatedAt),
						AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
					}})
				}
				t, i, err := upsertContained(st, op, page)
				if err != nil {
					return total, inserted, err
				}
				total += t
				inserted += i
			}
			return total, inserted, nil
		})
}

// isDataExchangeNotFound matches a data set or revision deleted between the
// phase that listed it and the call listing its children.
func isDataExchangeNotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// scanDataExchangeReceivedDataGrants lists the data grants sent to this
// account, in every acceptance state the SDK knows: the filter is passed
// explicitly because the API does not document what an empty one returns.
//
// A received grant reports the grant's own ARN, the ARN its sender's
// ListDataGrants reports. Identity includes the account, so the two rows can
// only meet in an account that grants to itself, which is unverified. The
// NativeID appends /received to guard that case, as servicecatalog's accepted
// portfolio shares append /accepted-share.
func scanDataExchangeReceivedDataGrants(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := dataexchange.NewListReceivedDataGrantsPaginator(client, &dataexchange.ListReceivedDataGrantsInput{
		AcceptanceState: dxtypes.AcceptanceStateFilterValue("").Values(),
	})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "dataexchange:ListReceivedDataGrants", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("dataexchange:ListReceivedDataGrants: %w", perr)
		}
		for _, g := range out.DataGrantSummaries {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			name := sv(g.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataExchangeReceivedDataGrant, NativeID: arn + "/received",
				Name: &name, Region: &region, Status: nonEmptyPtr(string(g.AcceptanceState)),
				CreatedAt:      tp(g.CreatedAt),
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dataexchange received-data-grants")
}

// dxRevisionStatus names a revision's lifecycle state from its flags. Revoked
// is checked first: a revoked revision no longer reaches subscribers whatever
// its Finalized flag says.
func dxRevisionStatus(r dxtypes.RevisionEntry) string {
	switch {
	case r.Revoked:
		return "REVOKED"
	case r.Finalized:
		return "FINALIZED"
	default:
		return "DRAFT"
	}
}
