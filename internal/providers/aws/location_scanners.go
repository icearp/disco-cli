package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/location"
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
}

func locARN(region, acct, kind, name string) string {
	return fmt.Sprintf("arn:aws:geo:%s:%s:%s/%s", region, acct, kind, name)
}

func scanLocation(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := location.NewFromConfig(acct.cfg, func(o *location.Options) { o.Region = region })

	var collections []childParent
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanLocationKeys(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			collections, t, i, err = scanLocationGeofenceCollections(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanLocationMaps(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLocationPlaceIndexes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanLocationRouteCalculators(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	trackerNames, t, i, ferr := scanLocationTrackers(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	for _, tn := range trackerNames {
		t, i, perr := scanLocationTrackerConsumers(ctx, client, acct, region, st, scanID, tn)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	t, i, err = scanLocationGeofences(ctx, client, acct, region, st, scanID, collections)
	return total + t, inserted + i, err
}

func scanLocationKeys(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := location.NewListKeysPaginator(client, &location.ListKeysInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "geo:ListKeys", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("geo:ListKeys: %w", perr)
		}
		for _, k := range out.Entries {
			name := sv(k.KeyName)
			if name == "" {
				continue
			}
			arn := locARN(region, acct.ID, "api-key", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLocationAPIKey, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(k), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "location api-keys")
}

// scanLocationGeofenceCollections stores the collections and returns them as
// the parents scanLocationGeofences fans out over.
func scanLocationGeofenceCollections(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := location.NewListGeofenceCollectionsPaginator(client, &location.ListGeofenceCollectionsInput{})
	var batch []*store.Resource
	var parents []childParent
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "geo:ListGeofenceCollections", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("geo:ListGeofenceCollections: %w", perr)
		}
		for _, c := range out.Entries {
			name := sv(c.CollectionName)
			if name == "" {
				continue
			}
			arn := locARN(region, acct.ID, "geofence-collection", name)
			label := name
			parents = append(parents, childParent{id: name, arn: arn})
			// Enrich via DescribeGeofenceCollection — list summary lacks
			// KmsKeyId; KMS edge needs Describe body.
			var attrsJSON string
			cn := name
			if dout, derr := client.DescribeGeofenceCollection(ctx, &location.DescribeGeofenceCollectionInput{CollectionName: &cn}); derr == nil {
				attrsJSON = mustJSON(dout)
			} else {
				attrsJSON = mustJSON(c)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLocationGeofenceCollection, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "location geofence-collections")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, t, i, nil
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
	const op = "geo:ListGeofences"
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
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

func scanLocationMaps(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := location.NewListMapsPaginator(client, &location.ListMapsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "geo:ListMaps", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("geo:ListMaps: %w", perr)
		}
		for _, m := range out.Entries {
			name := sv(m.MapName)
			if name == "" {
				continue
			}
			arn := locARN(region, acct.ID, "map", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLocationMap, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "location maps")
}

func scanLocationPlaceIndexes(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := location.NewListPlaceIndexesPaginator(client, &location.ListPlaceIndexesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "geo:ListPlaceIndexes", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("geo:ListPlaceIndexes: %w", perr)
		}
		for _, p := range out.Entries {
			name := sv(p.IndexName)
			if name == "" {
				continue
			}
			arn := locARN(region, acct.ID, "place-index", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLocationPlaceIndex, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "location place-indexes")
}

func scanLocationRouteCalculators(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := location.NewListRouteCalculatorsPaginator(client, &location.ListRouteCalculatorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "geo:ListRouteCalculators", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("geo:ListRouteCalculators: %w", perr)
		}
		for _, r := range out.Entries {
			name := sv(r.CalculatorName)
			if name == "" {
				continue
			}
			arn := locARN(region, acct.ID, "route-calculator", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLocationRouteCalculator, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "location route-calculators")
}

func scanLocationTrackers(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := location.NewListTrackersPaginator(client, &location.ListTrackersInput{})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "geo:ListTrackers", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("geo:ListTrackers: %w", perr)
		}
		for _, t := range out.Entries {
			name := sv(t.TrackerName)
			if name == "" {
				continue
			}
			arn := locARN(region, acct.ID, "tracker", name)
			label := name
			names = append(names, name)
			// Enrich via DescribeTracker — list summary lacks KmsKeyId.
			var attrsJSON string
			tn := name
			if dout, derr := client.DescribeTracker(ctx, &location.DescribeTrackerInput{TrackerName: &tn}); derr == nil {
				attrsJSON = mustJSON(dout)
			} else {
				attrsJSON = mustJSON(t)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLocationTracker, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "location trackers")
	return names, t, i, err
}

func scanLocationTrackerConsumers(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID, trackerName string) (int, int, error) {
	tn := trackerName
	pager := location.NewListTrackerConsumersPaginator(client, &location.ListTrackerConsumersInput{TrackerName: &tn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "geo:ListTrackerConsumers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("geo:ListTrackerConsumers: %w", perr)
		}
		for _, consumerARN := range out.ConsumerArns {
			if consumerARN == "" {
				continue
			}
			trackerARN := locARN(region, acct.ID, "tracker", trackerName)
			arn := fmt.Sprintf("%s/consumer/%s", trackerARN, consumerARN)
			label := consumerARN
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLocationTrackerConsumer, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"TrackerName": trackerName, "ConsumerArn": consumerARN}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "location tracker-consumers")
}
