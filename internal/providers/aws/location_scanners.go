package aws

import (
	"context"
	"fmt"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/location"
	loctypes "github.com/aws/aws-sdk-go-v2/service/location/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeLocationAPIKey, Service: "location"})
	registerType(restype.Descriptor{Type: TypeLocationGeofenceCollection, Service: "location"})
	registerType(restype.Descriptor{Type: TypeLocationGeofence, Service: "location"})
	registerType(restype.Descriptor{Type: TypeLocationMap, Service: "location"})
	registerType(restype.Descriptor{Type: TypeLocationPlaceIndex, Service: "location"})
	registerType(restype.Descriptor{Type: TypeLocationRouteCalculator, Service: "location"})
	registerType(restype.Descriptor{Type: TypeLocationTracker, Service: "location"})
	registerType(restype.Descriptor{Type: TypeLocationTrackerConsumer, Service: "location"})
	registerService(serviceEntry{
		name: "aws:location",
		fn:   scanLocation,
	})
}

type locationAPI interface {
	ListKeys(context.Context, *location.ListKeysInput, ...func(*location.Options)) (*location.ListKeysOutput, error)
	ListGeofenceCollections(context.Context, *location.ListGeofenceCollectionsInput, ...func(*location.Options)) (*location.ListGeofenceCollectionsOutput, error)
	ListGeofences(context.Context, *location.ListGeofencesInput, ...func(*location.Options)) (*location.ListGeofencesOutput, error)
	ListMaps(context.Context, *location.ListMapsInput, ...func(*location.Options)) (*location.ListMapsOutput, error)
	ListPlaceIndexes(context.Context, *location.ListPlaceIndexesInput, ...func(*location.Options)) (*location.ListPlaceIndexesOutput, error)
	ListRouteCalculators(context.Context, *location.ListRouteCalculatorsInput, ...func(*location.Options)) (*location.ListRouteCalculatorsOutput, error)
	ListTrackers(context.Context, *location.ListTrackersInput, ...func(*location.Options)) (*location.ListTrackersOutput, error)
	ListTrackerConsumers(context.Context, *location.ListTrackerConsumersInput, ...func(*location.Options)) (*location.ListTrackerConsumersOutput, error)
	DescribeGeofenceCollection(context.Context, *location.DescribeGeofenceCollectionInput, ...func(*location.Options)) (*location.DescribeGeofenceCollectionOutput, error)
	DescribeTracker(context.Context, *location.DescribeTrackerInput, ...func(*location.Options)) (*location.DescribeTrackerOutput, error)
	ListTagsForResource(context.Context, *location.ListTagsForResourceInput, ...func(*location.Options)) (*location.ListTagsForResourceOutput, error)
}

const (
	locListKeysOp             = "geo:ListKeys"
	locListCollectionsOp      = "geo:ListGeofenceCollections"
	locListMapsOp             = "geo:ListMaps"
	locListPlaceIndexesOp     = "geo:ListPlaceIndexes"
	locListRouteCalculatorsOp = "geo:ListRouteCalculators"
	locListTrackersOp         = "geo:ListTrackers"
	locListTrackerConsumersOp = "geo:ListTrackerConsumers"
	locListGeofencesOp        = "geo:ListGeofences"
	locTagsOp                 = "geo:ListTagsForResource"
	// locPageLimit is the MaxResults range maximum the model gives every
	// list op here.
	locPageLimit = 100
	// locNoName names, in a warning, a listed element without the name its
	// ARN is built from.
	locNoName = "(an element with no name)"
)

// The parts of Amazon Location Service the listings read, each on its own
// host (the ops' EndpointHostPrefix in the SDK): cp.metadata. (API keys, and
// ListTagsForResource for every type), cp.geofencing., cp.maps., cp.places.,
// cp.routes. and cp.tracking. ListGeofences calls the data-plane host
// geofencing. but is grouped with the collections it reads: a collection
// stored this scan has geofences to list, so an NXDOMAIN there is a failure,
// not a part of the service missing from the region.
const (
	locMetadataHost = iota
	locGeofencingHost
	locMapsHost
	locPlacesHost
	locRoutesHost
	locTrackingHost
)

// locNotFound is the not-found code ListTagsForResource, both Describes and
// the per-parent listings model, for a resource deleted after it was listed.
var locNotFound = []string{"ResourceNotFoundException"}

func locARN(partition, region, acct, kind, name string) string {
	return arnFor(partition, "geo", region, acct, kind+"/"+name)
}

func scanLocation(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := location.NewFromConfig(acct.cfg, func(o *location.Options) { o.Region = region })
	return scanLocationAll(ctx, client, acct, region, st, scanID)
}

// scanLocationAll runs every listing as its own phase, one at a time, in the
// order below, the tracker consumers and geofences after the trackers and
// collections stored this scan (runHostPhases): a phase's error does not
// stop the later ones; a failed write and ctx's end do. A part whose first
// listing stores nothing and answers only NXDOMAIN is not served in this
// region and its later listings are not run. The API-key part is the
// primary: its NXDOMAIN reports the region unavailable when no part stored
// a row, and is an error once one did. Each tagged listing writes a page at
// a time, and the tag denials of the whole scan are one warning.
//
// Geofences and tracker consumers carry no tags: neither has a resource type
// in the Service Reference, ListTagsForResource and TagResource name neither,
// and PutGeofence and AssociateTrackerConsumer take none.
func scanLocationAll(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := locationTags(client).sharingDenials()
	var collections, trackers []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "location", locMetadataHost, []hostPhase{
		{op: locListKeysOp, host: locMetadataHost, run: func(p *phaseTotals) bool {
			return scanLocationKeys(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: locListCollectionsOp, host: locGeofencingHost, run: func(p *phaseTotals) (failed bool) {
			collections, failed = scanLocationGeofenceCollections(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: locListMapsOp, host: locMapsHost, run: func(p *phaseTotals) bool {
			return scanLocationMaps(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: locListPlaceIndexesOp, host: locPlacesHost, run: func(p *phaseTotals) bool {
			return scanLocationPlaceIndexes(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: locListRouteCalculatorsOp, host: locRoutesHost, run: func(p *phaseTotals) bool {
			return scanLocationRouteCalculators(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: locListTrackersOp, host: locTrackingHost, run: func(p *phaseTotals) (failed bool) {
			trackers, failed = scanLocationTrackers(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(locTrackingHost, locListTrackerConsumersOp, func() (int, int, error) {
			return scanLocationTrackerConsumers(ctx, client, acct, region, st, scanID, trackers)
		}),
		countedPhase(locGeofencingHost, locListGeofencesOp, func() (int, int, error) {
			return scanLocationGeofences(ctx, client, acct, region, st, scanID, collections)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// locationTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: no listing here returns an ARN, so it is the ARN locARN builds
// from the listed name, in the Service Reference's ARN format for its type
// (arn:${Partition}:geo:${Region}:${Account}:<type>/<name>). The Service
// Reference lists every type read here as one ListTagsForResource takes,
// and each type's Create op takes Tags. The op does not page.
func locationTags(client locationAPI) rowTagReader {
	return rowTagReader{
		op:       locTagsOp,
		notFound: locNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &location.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// locationRow is the row of a listed element of typ, keyed by the ARN built
// from its name; kind is the ARN's resource type. An element without a name
// is not stored; what names the elements in the listing's one warning.
func locationRow(acct *account, region, scanID, typ, kind, what, name string, created *time.Time, item any) (*store.Resource, string, string) {
	if name == "" {
		return nil, what + " have no name", locNoName
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: locARN(acct.partition(), region, acct.ID, kind, name),
		Name: &name, Region: &region, CreatedAt: tp(created),
		AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

func scanLocationKeys(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, locListKeysOp,
		func(ctx context.Context, token *string) ([]loctypes.ListKeysResponseEntry, *string, error) {
			out, err := client.ListKeys(ctx, &location.ListKeysInput{MaxResults: sdkaws.Int32(locPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Entries, out.NextToken, nil
		},
		func(k loctypes.ListKeysResponseEntry) (*store.Resource, string, string) {
			return locationRow(acct, region, scanID, TypeLocationAPIKey, "api-key", "API keys", sv(k.KeyName), k.CreateTime, k)
		}, tags, p)
}

// storeLocationDescribed stores a listing a page at a time with each row
// replaced by its Describe body (describe, sent the row's name), which
// carries what the list element lacks (KmsKeyId) and the resource's tags
// (tokenPageSteps.detailTags). A resource the Describe no longer finds is
// dropped; any other Describe failure keeps the list element as the row
// (rowDetailReader) and reads its tags with ListTagsForResource instead. It
// returns the rows stored as the parents of a later phase, keyed by name, or
// storeWriteFailed (and none).
func storeLocationDescribed[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op, describeOp, what string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (*store.Resource, string, string),
	describe func(ctx context.Context, name string) (body any, tags map[string]string, err error),
	tags rowTagReader, p *phaseTotals,
) (parents []childParent, storeWriteFailed bool) {
	steps := tokenPageSteps{
		details: rowDetailReader{op: describeOp, what: what, notFound: locNotFound},
		detailTags: func(ctx context.Context, r *store.Resource) (taggedDetail, error) {
			body, t, err := describe(ctx, sv(r.Name))
			if err != nil {
				return taggedDetail{}, err
			}
			return taggedDetail{body: body, tags: mapTagsJSON(t), tagged: true}, nil
		},
		stored: func(rows []*store.Resource) {
			for _, r := range rows {
				parents = append(parents, childParent{id: sv(r.Name), arn: r.NativeID})
			}
		},
	}
	if storeTokenPagesTagsWith(ctx, st, acct, region, op, page, row, steps, tags, p) {
		return nil, true
	}
	return parents, false
}

// scanLocationGeofenceCollections stores the collections and returns them as
// the parents scanLocationGeofences fans out over.
func scanLocationGeofenceCollections(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeLocationDescribed(ctx, st, acct, region, locListCollectionsOp, "geo:DescribeGeofenceCollection", "geofence collection(s)",
		func(ctx context.Context, token *string) ([]loctypes.ListGeofenceCollectionsResponseEntry, *string, error) {
			out, err := client.ListGeofenceCollections(ctx, &location.ListGeofenceCollectionsInput{MaxResults: sdkaws.Int32(locPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Entries, out.NextToken, nil
		},
		func(c loctypes.ListGeofenceCollectionsResponseEntry) (*store.Resource, string, string) {
			return locationRow(acct, region, scanID, TypeLocationGeofenceCollection, "geofence-collection", "geofence collections", sv(c.CollectionName), c.CreateTime, c)
		},
		func(ctx context.Context, name string) (any, map[string]string, error) {
			out, err := client.DescribeGeofenceCollection(ctx, &location.DescribeGeofenceCollectionInput{CollectionName: &name})
			if err != nil {
				return nil, nil, err
			}
			return out, out.Tags, nil
		}, tags, p)
}

// scanLocationGeofences lists the geofences of each collection stored this
// scan. Geofences have no ARN; the NativeID is
// {collectionARN}/geofence/{GeofenceId}, unambiguous because the model's
// patterns let neither a collection name nor a geofence id contain "/".
//
// The geometry is stored whole, Geobuf included (base64): it is the geofence's
// configuration, so a digest or a cut would hide what a diff must show.
// Polygon and MultiPolygon are capped at 1,000 vertices; only Geobuf (up to
// 100,000 vertices) makes a row large. Each page is stored as it arrives, so
// memory holds at most one page (100 rows) per collection, times fanoutMed
// collections at once, not a whole collection of up to 50,000 geofences.
//
// GeofenceProperties is @sensitive in the model but is stored: it is customer
// metadata (at most 3 short key/value pairs) and carries no credential by
// contract, and redaction is for credentials.
//
// A DELETED geofence has left the system index and is dropped, as omics drops
// DELETED shares; a DELETING one still exists and is stored.
func scanLocationGeofences(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string, collections []childParent) (int, int, error) {
	const op = locListGeofencesOp
	return childFanOutStored(ctx, st, acct, region, op, collections, isLocationNotFound,
		func(ctx context.Context, p childParent) (int, int, error) {
			var total, inserted int
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			pager := location.NewListGeofencesPaginator(client, &location.ListGeofencesInput{CollectionName: &p.id},
				func(o *location.ListGeofencesPaginatorOptions) { o.Limit = 100 })
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return total, inserted, err
				}
				var page []containedChild
				for _, g := range out.Entries {
					id := sv(g.GeofenceId)
					if id == "" || sv(g.Status) == "DELETED" {
						continue
					}
					page = append(page, containedChild{parentID: parentID, row: &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeLocationGeofence, NativeID: p.arn + "/geofence/" + id,
						Name: &id, Status: nonEmptyPtr(sv(g.Status)), Region: &region,
						CreatedAt: tp(g.CreateTime), AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
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

// isLocationNotFound matches a collection deleted between
// ListGeofenceCollections and ListGeofences.
func isLocationNotFound(err error) bool {
	return isAPIErrorCode(err, locNotFound...)
}

func scanLocationMaps(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, locListMapsOp,
		func(ctx context.Context, token *string) ([]loctypes.ListMapsResponseEntry, *string, error) {
			out, err := client.ListMaps(ctx, &location.ListMapsInput{MaxResults: sdkaws.Int32(locPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Entries, out.NextToken, nil
		},
		func(m loctypes.ListMapsResponseEntry) (*store.Resource, string, string) {
			return locationRow(acct, region, scanID, TypeLocationMap, "map", "maps", sv(m.MapName), m.CreateTime, m)
		}, tags, p)
}

func scanLocationPlaceIndexes(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, locListPlaceIndexesOp,
		func(ctx context.Context, token *string) ([]loctypes.ListPlaceIndexesResponseEntry, *string, error) {
			out, err := client.ListPlaceIndexes(ctx, &location.ListPlaceIndexesInput{MaxResults: sdkaws.Int32(locPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Entries, out.NextToken, nil
		},
		func(x loctypes.ListPlaceIndexesResponseEntry) (*store.Resource, string, string) {
			return locationRow(acct, region, scanID, TypeLocationPlaceIndex, "place-index", "place indexes", sv(x.IndexName), x.CreateTime, x)
		}, tags, p)
}

func scanLocationRouteCalculators(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, locListRouteCalculatorsOp,
		func(ctx context.Context, token *string) ([]loctypes.ListRouteCalculatorsResponseEntry, *string, error) {
			out, err := client.ListRouteCalculators(ctx, &location.ListRouteCalculatorsInput{MaxResults: sdkaws.Int32(locPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Entries, out.NextToken, nil
		},
		func(r loctypes.ListRouteCalculatorsResponseEntry) (*store.Resource, string, string) {
			return locationRow(acct, region, scanID, TypeLocationRouteCalculator, "route-calculator", "route calculators", sv(r.CalculatorName), r.CreateTime, r)
		}, tags, p)
}

// scanLocationTrackers stores the trackers and returns them as the parents
// scanLocationTrackerConsumers reads.
func scanLocationTrackers(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeLocationDescribed(ctx, st, acct, region, locListTrackersOp, "geo:DescribeTracker", "tracker(s)",
		func(ctx context.Context, token *string) ([]loctypes.ListTrackersResponseEntry, *string, error) {
			out, err := client.ListTrackers(ctx, &location.ListTrackersInput{MaxResults: sdkaws.Int32(locPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Entries, out.NextToken, nil
		},
		func(t loctypes.ListTrackersResponseEntry) (*store.Resource, string, string) {
			return locationRow(acct, region, scanID, TypeLocationTracker, "tracker", "trackers", sv(t.TrackerName), t.CreateTime, t)
		},
		func(ctx context.Context, name string) (any, map[string]string, error) {
			out, err := client.DescribeTracker(ctx, &location.DescribeTrackerInput{TrackerName: &name})
			if err != nil {
				return nil, nil, err
			}
			return out, out.Tags, nil
		}, tags, p)
}

// scanLocationTrackerConsumers stores the consumers (geofence collection
// ARNs) of each tracker stored this scan, the trackers read concurrently
// (storeTokenEachParent). The NativeID is {trackerARN}/consumer/{ConsumerArn}.
func scanLocationTrackerConsumers(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string, trackers []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, locListTrackerConsumersOp, locNotFound, childParentIDs(trackers),
		func(ctx context.Context, tracker string, token *string) ([]string, *string, error) {
			out, err := client.ListTrackerConsumers(ctx, &location.ListTrackerConsumersInput{TrackerName: &tracker, MaxResults: sdkaws.Int32(locPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ConsumerArns, out.NextToken, nil
		},
		func(tracker, consumerARN string) (*store.Resource, string, string) {
			if consumerARN == "" {
				return nil, "tracker consumers have an empty ARN", tracker
			}
			trackerARN := locARN(acct.partition(), region, acct.ID, "tracker", tracker)
			label := consumerARN
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLocationTrackerConsumer, NativeID: fmt.Sprintf("%s/consumer/%s", trackerARN, consumerARN),
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"TrackerName": tracker, "ConsumerArn": consumerARN}), DiscoveredBy: scanID,
			}, "", ""
		})
}
