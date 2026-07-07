package gcp

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/logging/v2"
	"google.golang.org/api/monitoring/v3"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:logging",
		fn:   scanLogging,
		emits: []coverage.TypeDecl{
			{Service: "logging", DiscoType: TypeLoggingSink},
			{Service: "logging", DiscoType: TypeLoggingBucket},
			{Service: "logging", DiscoType: TypeLoggingExclusion},
			{Service: "logging", DiscoType: TypeLoggingMetric},
			{Service: "logging", DiscoType: TypeLoggingLink},
			{Service: "logging", DiscoType: TypeLoggingView},
			{Service: "logging", DiscoType: TypeLoggingLogScope},
			{Service: "logging", DiscoType: TypeLoggingSavedQuery},
		},
	})
	registerService(serviceEntry{
		name: "gcp:monitoring",
		fn:   scanMonitoringAlertPolicies,
		emits: []coverage.TypeDecl{
			{Service: "monitoring", DiscoType: TypeMonitoringAlertPol},
		},
	})
}

// maxConcurrentLoggingFanout caps per-bucket Link/View fan-out. Per-project
// bucket count is low (a handful of custom buckets plus the two built-ins);
// keep modest like DNS's per-zone fan-out.
const maxConcurrentLoggingFanout = 10

// scanLogging discovers Cloud Logging sinks, buckets, exclusions, metrics,
// per-bucket links/views, log scopes, and saved queries. Folder + organization
// scope sinks are handled separately by scanLoggingSinksOrg.
//
//  1. Sinks — flat, project-parented.
//  2. Buckets — wildcard `locations/-` (SDK doc confirms wildcard support),
//     one paginated walk across every location. Bucket refs collected for
//     phase 3's per-bucket fan-out.
//  3. Links, Views — fan out per already-scanned bucket (no wildcard parent
//     for either; both require an explicit `.../buckets/{bucket}`).
//  4. Exclusions, Metrics — flat, project-parented.
//  5. LogScopes — literal `locations/global`, not a wildcard: the SDK's
//     LogScope.Name doc states "Log scopes are only available in the global
//     location", so there is no other location to fan out across.
//  6. SavedQueries — wildcard `locations/-` (SDK doc confirms wildcard
//     support).
func scanLogging(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := logging.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("logging client: %w", err)
	}

	t, n, err := scanLoggingSinks(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	bucketIDs, t, n, err := scanLoggingBuckets(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanLoggingBucketLinks(ctx, svc, p, bucketIDs, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanLoggingBucketViews(ctx, svc, p, bucketIDs, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanLoggingExclusions(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanLoggingMetrics(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanLoggingLogScopes(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanLoggingSavedQueries(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	return total, inserted, err
}

// scanLoggingSinks discovers Cloud Logging sinks.
func scanLoggingSinks(ctx context.Context, svc *logging.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	return runPaginated(ctx, st, p, "logging:sinks.list",
		svc.Projects.Sinks.List(parent),
		func(page *logging.ListSinksResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Sinks))
			for _, s := range page.Sinks {
				name := s.Name
				// Sinks have no fully-qualified resource name — synthesize
				// `projects/{p}/sinks/{name}` for stable NativeID.
				nativeID := fmt.Sprintf("projects/%s/sinks/%s", p.ID, s.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeLoggingSink,
					NativeID:       nativeID,
					Name:           &name,
					CreatedAt:      strp(s.CreateTime),
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanLoggingBuckets discovers log buckets across every location via the
// wildcard parent. Returns bucket NativeIDs for phase 3's per-bucket
// Link/View fan-out.
func scanLoggingBuckets(ctx context.Context, svc *logging.Service, p *project, st *store.Store, scanID string) (bucketIDs []string, total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	total, inserted, err = runPaginated(ctx, st, p, "logging:buckets.list",
		svc.Projects.Locations.Buckets.List(parent),
		func(page *logging.ListBucketsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Buckets))
			for _, b := range page.Buckets {
				if b == nil || b.Name == "" {
					continue
				}
				bucketIDs = append(bucketIDs, b.Name)
				region := locationFromResourceName(b.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					Region:         &region,
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeLoggingBucket,
					NativeID:       b.Name,
					CreatedAt:      strp(b.CreateTime),
					AttributesJSON: mustJSON(b),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	return bucketIDs, total, inserted, err
}

// scanLoggingBucketLinks fans out Links.List per already-scanned bucket
// (no wildcard parent support — requires an explicit bucket resource name).
func scanLoggingBucketLinks(ctx context.Context, svc *logging.Service, p *project, bucketIDs []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentLoggingFanout, bucketIDs, func(gctx context.Context, bucketName string) error {
		bucketResID := store.ResourceID("gcp", p.ID, TypeLoggingBucket, bucketName)
		var batch []*store.Resource
		listErr := svc.Projects.Locations.Buckets.Links.List(bucketName).Pages(gctx, func(page *logging.ListLinksResponse) error {
			for _, l := range page.Links {
				if l == nil || l.Name == "" {
					continue
				}
				region := locationFromResourceName(l.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					Region:         &region,
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeLoggingLink,
					NativeID:       l.Name,
					CreatedAt:      strp(l.CreateTime),
					AttributesJSON: mustJSON(l),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "logging:buckets.links.list", bucketName, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, bucketResID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanLoggingBucketViews fans out Views.List per already-scanned bucket
// (same shape as Links — no wildcard parent support).
func scanLoggingBucketViews(ctx context.Context, svc *logging.Service, p *project, bucketIDs []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentLoggingFanout, bucketIDs, func(gctx context.Context, bucketName string) error {
		bucketResID := store.ResourceID("gcp", p.ID, TypeLoggingBucket, bucketName)
		var batch []*store.Resource
		listErr := svc.Projects.Locations.Buckets.Views.List(bucketName).Pages(gctx, func(page *logging.ListViewsResponse) error {
			for _, v := range page.Views {
				if v == nil || v.Name == "" {
					continue
				}
				region := locationFromResourceName(v.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					Region:         &region,
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeLoggingView,
					NativeID:       v.Name,
					CreatedAt:      strp(v.CreateTime),
					AttributesJSON: mustJSON(v),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "logging:buckets.views.list", bucketName, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, bucketResID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanLoggingExclusions discovers project-level log exclusion filters.
func scanLoggingExclusions(ctx context.Context, svc *logging.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	return runPaginated(ctx, st, p, "logging:exclusions.list",
		svc.Projects.Exclusions.List(parent),
		func(page *logging.ListExclusionsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Exclusions))
			for _, e := range page.Exclusions {
				if e == nil || e.Name == "" {
					continue
				}
				name := e.Name
				nativeID := fmt.Sprintf("projects/%s/exclusions/%s", p.ID, e.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeLoggingExclusion,
					NativeID:       nativeID,
					Name:           &name,
					CreatedAt:      strp(e.CreateTime),
					AttributesJSON: mustJSON(e),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanLoggingMetrics discovers user-defined logs-based metrics.
func scanLoggingMetrics(ctx context.Context, svc *logging.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	return runPaginated(ctx, st, p, "logging:metrics.list",
		svc.Projects.Metrics.List(parent),
		func(page *logging.ListLogMetricsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Metrics))
			for _, m := range page.Metrics {
				if m == nil || m.Name == "" {
					continue
				}
				name := m.Name
				// Metric identifiers may contain "/" (SDK doc example:
				// "nginx/requests"), so hand-building the resource name from
				// Name would mis-nest it — use the SDK-populated,
				// already-URL-encoded ResourceName instead.
				nativeID := m.ResourceName
				if nativeID == "" {
					nativeID = fmt.Sprintf("projects/%s/metrics/%s", p.ID, m.Name)
				}
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeLoggingMetric,
					NativeID:       nativeID,
					Name:           &name,
					AttributesJSON: mustJSON(m),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanLoggingLogScopes discovers log scopes. Log scopes exist only in the
// global location (LogScope.Name SDK doc), so the parent is the literal
// `locations/global` — no wildcard, no per-location fan-out needed.
func scanLoggingLogScopes(ctx context.Context, svc *logging.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s/locations/global", p.ID)
	return runPaginated(ctx, st, p, "logging:logScopes.list",
		svc.Projects.Locations.LogScopes.List(parent),
		func(page *logging.ListLogScopesResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.LogScopes))
			for _, l := range page.LogScopes {
				if l == nil || l.Name == "" {
					continue
				}
				region := locationFromResourceName(l.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					Region:         &region,
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeLoggingLogScope,
					NativeID:       l.Name,
					CreatedAt:      strp(l.CreateTime),
					AttributesJSON: mustJSON(l),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanLoggingSavedQueries discovers saved log queries across every location
// via the wildcard parent.
func scanLoggingSavedQueries(ctx context.Context, svc *logging.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s/locations/-", p.ID)
	return runPaginated(ctx, st, p, "logging:savedQueries.list",
		svc.Projects.Locations.SavedQueries.List(parent),
		func(page *logging.ListSavedQueriesResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.SavedQueries))
			for _, q := range page.SavedQueries {
				if q == nil || q.Name == "" {
					continue
				}
				name := q.DisplayName
				region := locationFromResourceName(q.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					Region:         &region,
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeLoggingSavedQuery,
					NativeID:       q.Name,
					Name:           &name,
					CreatedAt:      strp(q.CreateTime),
					AttributesJSON: mustJSON(q),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanMonitoringAlertPolicies discovers Cloud Monitoring alert policies.
// Notification channels (alert.notificationChannels[]) aren't scanned yet —
// would need a sibling Channels.List call; deferred to an alert-policy →
// channel resolver follow-up.
func scanMonitoringAlertPolicies(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := monitoring.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("monitoring client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s", p.ID)
	return runPaginated(ctx, st, p, "monitoring:alertPolicies.list",
		svc.Projects.AlertPolicies.List(parent),
		func(page *monitoring.ListAlertPoliciesResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.AlertPolicies))
			for _, a := range page.AlertPolicies {
				name := a.DisplayName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeMonitoringAlertPol,
					NativeID:       a.Name,
					Name:           &name,
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}
