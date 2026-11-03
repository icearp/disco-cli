package aws

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

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
	ListTagsForResource(context.Context, *dataexchange.ListTagsForResourceInput, ...func(*dataexchange.Options)) (*dataexchange.ListTagsForResourceOutput, error)
}

const (
	adxTagsOp = "dataexchange:ListTagsForResource"
	// adxPageCap is the MaxResults maximum the model sets for every Data
	// Exchange listing read here.
	adxPageCap = 200
	// adxReceivedSuffix ends a received grant's NativeID, after the grant's
	// ARN; see scanDataExchangeReceivedDataGrants.
	adxReceivedSuffix = "/received"
)

// adxParentGone is the not-found code ListDataSetRevisions and
// ListRevisionAssets model, for a data set or revision deleted since it was
// listed.
var adxParentGone = []string{"ResourceNotFoundException"}

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
// Every listing runs as its own phase, one at a time, in this order: a
// phase's error does not stop the later ones; a failed write and ctx's end
// do (runHostPhases). The revision phase lists the data sets the first phase
// stored, and the asset phase the revisions the revision phase stored. Every
// type has its tags read per row (adxTags), and the tag denials of the whole
// scan are one warning.
func scanDataExchange(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := dataexchange.NewFromConfig(acct.cfg, func(o *dataexchange.Options) { o.Region = region })
	tags := adxTags(client).sharingDenials()
	var (
		dataSets  []dxDataSet
		revisions []dxRevision
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "dataexchange", 0, []hostPhase{
		{op: "dataexchange:ListDataSets", run: func(p *phaseTotals) (failed bool) {
			dataSets, failed = scanDataExchangeDataSets(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "dataexchange:ListDataGrants", run: func(p *phaseTotals) bool {
			return scanDataExchangeDataGrants(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dataexchange:ListEventActions", run: func(p *phaseTotals) bool {
			return scanDataExchangeEventActions(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dataexchange:ListReceivedDataGrants", run: func(p *phaseTotals) bool {
			return scanDataExchangeReceivedDataGrants(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "dataexchange:ListDataSetRevisions", run: func(p *phaseTotals) (failed bool) {
			revisions, failed = scanDataExchangeRevisions(ctx, client, acct, region, st, scanID, dataSets, tags, p)
			return failed
		}},
		{op: "dataexchange:ListRevisionAssets", run: func(p *phaseTotals) bool {
			return scanDataExchangeAssets(ctx, client, acct, region, st, scanID, revisions, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// adxTags reads a row's tags with ListTagsForResource, one call per row,
// sent the row's NativeID, the ARN its listing returned. The Service
// Reference lists every type read here under ListTagsForResource (assets,
// data-grants, data-sets, event-actions, revisions); no listed element
// carries tags. The op answers with every tag at once (no page token). Its
// model lists no error, so no code drops a row: a resource deleted between
// its listing and its tag read keeps its row untagged, and the read's error
// is returned.
func adxTags(client dataExchangeAPI) rowTagReader {
	return rowTagReader{
		op: adxTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			return adxReadTags(ctx, client, r.NativeID)
		},
	}
}

// adxReadTags reads the tags of the resource arn names.
func adxReadTags(ctx context.Context, client dataExchangeAPI, arn string) (*string, error) {
	out, err := client.ListTagsForResource(ctx, &dataexchange.ListTagsForResourceInput{ResourceArn: &arn})
	if err != nil {
		return nil, err
	}
	return mapTagsJSON(out.Tags), nil
}

// adxRow is the row of a listed element stored under its ARN and named
// name, its attributes the element. An element without an ARN is not
// stored (arnRow): one empty NativeID would merge every such element into
// one resource; what names the elements in that warning.
func adxRow(acct *account, region, scanID, typ, what string, arn, name *string, status *string, created *time.Time, element any) (*store.Resource, string, string) {
	label := sv(name)
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: &label, Region: &region, Status: status, CreatedAt: tp(created),
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// scanDataExchangeDataSets stores the owned data sets with their tags, each
// page written as soon as its tags are read, and returns the stored ones
// that have an Id, the parents of the revision phase; one stored without is
// warned about, as stored without its revisions. Entitled (subscribed-from-
// others) data sets are catalogued separately upstream as entitled-data-sets
// and not scanned here. It reports whether a write failed, after which the
// scan must stop.
func scanDataExchangeDataSets(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]dxDataSet, bool) {
	assetTypes := map[string]dxtypes.AssetType{}
	parents, failed := storeParentPagesTags(ctx, st, acct, region, "dataexchange:ListDataSets", "data sets", "Id",
		func(ctx context.Context, token *string) ([]dxtypes.DataSetEntry, *string, error) {
			out, err := client.ListDataSets(ctx, &dataexchange.ListDataSetsInput{Origin: sdkaws.String("OWNED"), MaxResults: adxPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DataSets, out.NextToken, nil
		},
		func(d dxtypes.DataSetEntry) (*store.Resource, string, string) {
			return adxRow(acct, region, scanID, TypeDataExchangeDataSets, "data sets", d.Arn, d.Name, nil, nil, d)
		},
		func(d dxtypes.DataSetEntry) string {
			assetTypes[sv(d.Id)] = d.AssetType
			return sv(d.Id)
		},
		tags, p)
	if failed {
		return nil, true
	}
	dataSets := make([]dxDataSet, len(parents))
	for i, c := range parents {
		dataSets[i] = dxDataSet{childParent: c, assetType: assetTypes[c.id]}
	}
	return dataSets, false
}

// scanDataExchangeDataGrants stores the data grants this account sent, with
// their tags, each page written as soon as its tags are read. It reports
// whether a write failed, after which the scan must stop.
func scanDataExchangeDataGrants(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "dataexchange:ListDataGrants",
		func(ctx context.Context, token *string) ([]dxtypes.DataGrantSummaryEntry, *string, error) {
			out, err := client.ListDataGrants(ctx, &dataexchange.ListDataGrantsInput{MaxResults: sdkaws.Int32(adxPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DataGrantSummaries, out.NextToken, nil
		},
		func(g dxtypes.DataGrantSummaryEntry) (*store.Resource, string, string) {
			status := string(g.AcceptanceState)
			return adxRow(acct, region, scanID, TypeDataExchangeDataGrants, "data grants", g.Arn, g.Name, &status, nil, g)
		},
		tags, p)
}

// scanDataExchangeEventActions stores the event actions with their tags,
// each page written as soon as its tags are read. An event action has no
// name; it is named by its Id. It reports whether a write failed, after
// which the scan must stop.
func scanDataExchangeEventActions(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "dataexchange:ListEventActions",
		func(ctx context.Context, token *string) ([]dxtypes.EventActionEntry, *string, error) {
			out, err := client.ListEventActions(ctx, &dataexchange.ListEventActionsInput{MaxResults: adxPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EventActions, out.NextToken, nil
		},
		func(e dxtypes.EventActionEntry) (*store.Resource, string, string) {
			return adxRow(acct, region, scanID, TypeDataExchangeEventActions, "event actions", e.Arn, e.Id, nil, nil, e)
		},
		tags, p)
}

// scanDataExchangeReceivedDataGrants stores the data grants sent to this
// account, in every acceptance state the SDK knows, with their tags, each
// page written as soon as its tags are read: the filter is passed
// explicitly because the API does not document what an empty one returns.
// It reports whether a write failed, after which the scan must stop.
//
// A received grant reports the grant's own ARN, the ARN its sender's
// ListDataGrants reports. Identity includes the account, so the two rows can
// only meet in an account that grants to itself, which is unverified. The
// NativeID appends adxReceivedSuffix to guard that case, as servicecatalog's
// accepted portfolio shares append /accepted-share.
//
// The tag read is sent the grant's ARN, which names the sender's account: a
// grant is its sender's resource. A denied read of a grant another account
// sent gives no tags and no error (untaggedIfOtherAccount), so the row is
// kept untagged without a warning. Whether Data Exchange serves the
// receiver the sender's tags, or refuses with a denial, is unverified live.
func scanDataExchangeReceivedDataGrants(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		return adxReadTags(ctx, client, strings.TrimSuffix(r.NativeID, adxReceivedSuffix))
	}
	return storeTokenPagesTags(ctx, st, acct, region, "dataexchange:ListReceivedDataGrants",
		func(ctx context.Context, token *string) ([]dxtypes.ReceivedDataGrantSummariesEntry, *string, error) {
			out, err := client.ListReceivedDataGrants(ctx, &dataexchange.ListReceivedDataGrantsInput{
				AcceptanceState: dxtypes.AcceptanceStateFilterValue("").Values(),
				MaxResults:      sdkaws.Int32(adxPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.DataGrantSummaries, out.NextToken, nil
		},
		func(g dxtypes.ReceivedDataGrantSummariesEntry) (*store.Resource, string, string) {
			r, why, key := adxRow(acct, region, scanID, TypeDataExchangeReceivedDataGrant, "received data grants", g.Arn, g.Name, nonEmptyPtr(string(g.AcceptanceState)), g.CreatedAt, g)
			if r != nil {
				r.NativeID += adxReceivedSuffix
			}
			return r, why, key
		},
		tags.untaggedIfOtherAccount(acct.ID), p)
}

// adxUnder records each row a per-parent phase writes page by page
// (storeEachParentPagesTagsWith) under the parent it was listed under: what
// was listed of each row built, until the row is written, then the row's
// contains pair. The rows of different parents are built concurrently and
// written in no fixed order, so mu guards both.
type adxUnder struct {
	mu     sync.Mutex
	listed map[*store.Resource]adxListed
	pairs  [][2]string
}

// adxListed is what a row's listing gave: its parent's ARN and, for a
// revision, the revision as a parent of the asset phase.
type adxListed struct {
	parentARN string
	revision  dxRevision
}

// adxListedUnder notes that r was listed as l says.
func (u *adxUnder) adxListedUnder(r *store.Resource, l adxListed) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.listed == nil {
		u.listed = map[*store.Resource]adxListed{}
	}
	u.listed[r] = l
}

// adxWrittenUnder adds the contains pair of each written row and returns
// what was listed of the rows, in row order.
func (u *adxUnder) adxWrittenUnder(acctID string, rows []*store.Resource) []adxListed {
	u.mu.Lock()
	defer u.mu.Unlock()
	listed := make([]adxListed, len(rows))
	for i, r := range rows {
		listed[i] = u.listed[r]
		delete(u.listed, r)
		u.pairs = append(u.pairs, [2]string{r.ID, store.ResourceID("aws", acctID, listed[i].parentARN)})
	}
	return listed
}

// scanDataExchangeRevisions stores the revisions of each data set stored
// this scan with their tags, under the data set, each page written as soon
// as its tags are read because a data set gains a revision per publication;
// one data set's failure costs the others none of theirs. It returns the
// stored revisions whose assets scanDataExchangeAssets lists, sorted by ARN:
// those of data sets whose AssetType is not S3_SNAPSHOT. A data set's
// AssetType is fixed at CreateDataSet and names the type of asset added to
// it. Such a revision stored without an Id is warned about, as stored
// without its assets. It reports whether a write failed, after which the
// scan must stop.
//
// The hierarchy of the rows written is recorded in one closure write once
// the phase's pages are, so the phase's warnings concern rows already
// written whatever the closure write does. It is recorded after a failed
// row write too, so the rows written before it keep their contains edges.
func scanDataExchangeRevisions(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string, dataSets []dxDataSet, tags rowTagReader, p *phaseTotals) ([]dxRevision, bool) {
	const op = "dataexchange:ListDataSetRevisions"
	parents := make([]childParent, len(dataSets))
	listsAssets := make(map[string]bool, len(dataSets))
	dataSetARN := make(map[string]string, len(dataSets))
	for i, d := range dataSets {
		parents[i] = d.childParent
		listsAssets[d.id] = d.assetType != dxtypes.AssetTypeS3Snapshot
		dataSetARN[d.id] = d.arn
	}
	var (
		under     adxUnder
		revisions []dxRevision
		noID      skipTally
	)
	failed := storeEachParentPagesTagsWith(ctx, st, acct, region, op, adxParentGone, parents,
		func(ctx context.Context, dataSetID string, token *string) ([]dxtypes.RevisionEntry, *string, error) {
			out, err := client.ListDataSetRevisions(ctx, &dataexchange.ListDataSetRevisionsInput{DataSetId: &dataSetID, MaxResults: adxPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Revisions, out.NextToken, nil
		},
		func(dataSetID string, rev dxtypes.RevisionEntry) (*store.Resource, string, string) {
			r, why, key := adxRow(acct, region, scanID, TypeDataExchangeDataSetRevision, "revisions", rev.Arn, rev.Id, sp(dxRevisionStatus(rev)), rev.CreatedAt, rev)
			if r != nil {
				id := sv(rev.Id)
				under.adxListedUnder(r, adxListed{parentARN: dataSetARN[dataSetID], revision: dxRevision{
					childParent: childParent{id: dataSetID + "/" + id, arn: r.NativeID},
					dataSetID:   dataSetID, revisionID: id,
				}})
			}
			return r, why, key
		},
		nil,
		func(rows []*store.Resource) {
			for _, l := range under.adxWrittenUnder(acct.ID, rows) {
				switch rev := l.revision; {
				case !listsAssets[rev.dataSetID]:
				case rev.revisionID == "":
					noID.skippedLeast(rev.arn, nil)
				default:
					revisions = append(revisions, rev)
				}
			}
		},
		tags, p)
	if closureFailed := recordHierarchyPairs(st, op, under.pairs, p); failed || closureFailed {
		return nil, true
	}
	warnNoChildID(st, op, acct, region, &noID, "revisions of data sets holding assets", "Id")
	// The data sets' pages are written in no fixed order.
	slices.SortFunc(revisions, func(a, b dxRevision) int { return strings.Compare(a.arn, b.arn) })
	return revisions, false
}

// scanDataExchangeAssets stores the assets of each given revision with their
// tags, under the revision, each page written as soon as its tags are read;
// one revision's failure costs the others none of theirs. An asset is
// stored only when it is a means of fulfilling data — a Redshift datashare,
// API Gateway API, Lake Formation data permission or S3 data access — which
// points at infrastructure the provider runs and grants subscribers access
// to it. An S3 data access asset's Keys and KeyPrefixes are kept: they are
// the access the provider chose to share, not object contents. An
// S3_SNAPSHOT asset is a copy of one S3 object: the data itself, one row per
// file, so scanDataExchangeRevisions does not hand over the revisions of
// S3_SNAPSHOT data sets. It reports whether a write failed, after which the
// scan must stop. As in scanDataExchangeRevisions, the hierarchy of the rows
// written is recorded once the pages are, a failed row write's included.
func scanDataExchangeAssets(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string, revisions []dxRevision, tags rowTagReader, p *phaseTotals) bool {
	const op = "dataexchange:ListRevisionAssets"
	parents := make([]childParent, len(revisions))
	byID := make(map[string]dxRevision, len(revisions))
	for i, r := range revisions {
		parents[i] = r.childParent
		byID[r.id] = r
	}
	var under adxUnder
	failed := storeEachParentPagesTagsWith(ctx, st, acct, region, op, adxParentGone, parents,
		func(ctx context.Context, revID string, token *string) ([]dxtypes.AssetEntry, *string, error) {
			rev := byID[revID]
			out, err := client.ListRevisionAssets(ctx, &dataexchange.ListRevisionAssetsInput{
				DataSetId: &rev.dataSetID, RevisionId: &rev.revisionID, MaxResults: adxPageCap, NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Assets, out.NextToken, nil
		},
		func(revID string, a dxtypes.AssetEntry) (*store.Resource, string, string) {
			r, why, key := adxRow(acct, region, scanID, TypeDataExchangeRevisionAsset, "assets", a.Arn, a.Name, nil, a.CreatedAt, a)
			if r != nil {
				under.adxListedUnder(r, adxListed{parentARN: byID[revID].arn})
			}
			return r, why, key
		},
		nil,
		func(rows []*store.Resource) {
			under.adxWrittenUnder(acct.ID, rows)
		},
		tags, p)
	closureFailed := recordHierarchyPairs(st, op, under.pairs, p)
	return failed || closureFailed
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
