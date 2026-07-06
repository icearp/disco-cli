package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/location"
)

func init() {
	registerService(serviceEntry{
		name: "aws:location",
		fn:   scanLocation,
		emits: []coverage.TypeDecl{
			{Service: "location", DiscoType: TypeLocationAPIKey},
			{Service: "location", DiscoType: TypeLocationGeofenceCollection},
			{Service: "location", DiscoType: TypeLocationMap, Leaf: true},
			{Service: "location", DiscoType: TypeLocationPlaceIndex, Leaf: true},
			{Service: "location", DiscoType: TypeLocationRouteCalculator, Leaf: true},
			{Service: "location", DiscoType: TypeLocationTracker},
			{Service: "location", DiscoType: TypeLocationTrackerConsumer},
		},
	})
}

type locationAPI interface {
	ListKeys(context.Context, *location.ListKeysInput, ...func(*location.Options)) (*location.ListKeysOutput, error)
	ListGeofenceCollections(context.Context, *location.ListGeofenceCollectionsInput, ...func(*location.Options)) (*location.ListGeofenceCollectionsOutput, error)
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

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanLocationKeys(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanLocationGeofenceCollections(ctx, client, acct, region, st, scanID)
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
	return total, inserted, nil
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

func scanLocationGeofenceCollections(ctx context.Context, client locationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := location.NewListGeofenceCollectionsPaginator(client, &location.ListGeofenceCollectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "geo:ListGeofenceCollections", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("geo:ListGeofenceCollections: %w", perr)
		}
		for _, c := range out.Entries {
			name := sv(c.CollectionName)
			if name == "" {
				continue
			}
			arn := locARN(region, acct.ID, "geofence-collection", name)
			label := name
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
	return upsertBatch(st, batch, "location geofence-collections")
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
