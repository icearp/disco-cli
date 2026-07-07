package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/logging/v2"
	monitoringv1 "google.golang.org/api/monitoring/v1"
	"google.golang.org/api/monitoring/v3"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:logging",
		fn:   scanLogging,
		emits: []coverage.TypeDecl{
			{Service: "logging", DiscoType: TypeLoggingSink},
			{Service: "logging", DiscoType: TypeLoggingBucket},
			{Service: "logging", DiscoType: TypeLoggingExclusion, Leaf: true},
			{Service: "logging", DiscoType: TypeLoggingMetric},
			{Service: "logging", DiscoType: TypeLoggingLink},
			{Service: "logging", DiscoType: TypeLoggingView, Leaf: true},
			{Service: "logging", DiscoType: TypeLoggingLogScope},
			{Service: "logging", DiscoType: TypeLoggingSavedQuery, Leaf: true},
		},
	})
	registerService(serviceEntry{
		name: "gcp:monitoring",
		fn:   scanMonitoring,
		emits: []coverage.TypeDecl{
			{Service: "monitoring", DiscoType: TypeMonitoringAlertPol},
			{Service: "monitoring", DiscoType: TypeMonitoringDashboard},
			{Service: "monitoring", DiscoType: TypeMonitoringGroup},
			{Service: "monitoring", DiscoType: TypeMonitoringNotificationChannel, Leaf: true},
			{Service: "monitoring", DiscoType: TypeMonitoringService},
			{Service: "monitoring", DiscoType: TypeMonitoringSLO, Leaf: true},
			{Service: "monitoring", DiscoType: TypeMonitoringSnooze},
			{Service: "monitoring", DiscoType: TypeMonitoringUptimeCheckConfig},
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

// maxConcurrentMonitoringFanout caps per-Group Member and per-Service SLO
// fan-out. Per-project cardinality is low; keep modest like the DNS/logging
// per-zone/per-bucket fan-outs.
const maxConcurrentMonitoringFanout = 10

// scanMonitoring discovers Cloud Monitoring alert policies, dashboards,
// groups (with members embedded — see scanMonitoringGroups doc comment),
// notification channels, services + SLOs, snoozes, and uptime check configs.
// All flat/project-parented except SLOs (fan out per already-scanned
// Service) and Group Members (fanned out per Group but embedded, not
// upserted as an independent type — see scanMonitoringGroups).
func scanMonitoring(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := monitoring.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("monitoring client: %w", err)
	}
	dashSvc, err := monitoringv1.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("monitoring/v1 client: %w", err)
	}

	t, n, err := scanMonitoringAlertPolicies(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanMonitoringDashboards(ctx, dashSvc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanMonitoringGroups(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanMonitoringNotificationChannels(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	serviceIDs, t, n, err := scanMonitoringServices(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanMonitoringSLOs(ctx, svc, p, serviceIDs, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	t, n, err = scanMonitoringSnoozes(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanMonitoringUptimeCheckConfigs(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	return total, inserted, err
}

// scanMonitoringAlertPolicies discovers Cloud Monitoring alert policies.
// Notification channels (alert.notificationChannels[]) aren't scanned yet —
// would need a sibling Channels.List call; deferred to an alert-policy →
// channel resolver follow-up.
func scanMonitoringAlertPolicies(ctx context.Context, svc *monitoring.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
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

// scanMonitoringDashboards discovers custom dashboards (monitoring/v1 —
// dashboards live on a separate API version from every other monitoring
// type).
func scanMonitoringDashboards(ctx context.Context, svc *monitoringv1.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	return runPaginated(ctx, st, p, "monitoring:dashboards.list",
		svc.Projects.Dashboards.List(parent),
		func(page *monitoringv1.ListDashboardsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Dashboards))
			for _, d := range page.Dashboards {
				if d == nil || d.Name == "" {
					continue
				}
				name := d.DisplayName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeMonitoringDashboard,
					NativeID:       d.Name,
					Name:           &name,
					AttributesJSON: mustJSON(d),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanMonitoringGroups discovers groups and, per group, their monitored-
// resource membership. Group members have no SDK-issued name or ID of their
// own (MonitoredResource carries only Type + a Labels map describing
// whichever resource it refers to) — there's no natural key to give an
// independent resource row, so members are fetched at scan time and embedded
// under a "members" key in the owning Group's attributes, per the
// embed-child-data convention (internal/providers/CLAUDE.md) rather than
// promoted to their own type.
func scanMonitoringGroups(ctx context.Context, svc *monitoring.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	var groups []*monitoring.Group
	listErr := svc.Projects.Groups.List(parent).Pages(ctx, func(page *monitoring.ListGroupsResponse) error {
		groups = append(groups, page.Group...)
		return nil
	})
	if listErr != nil {
		if isPermissionDenied(listErr) {
			return 0, 0, skipIfDenied(st, "monitoring:groups.list", p.ID, listErr)
		}
		return 0, 0, listErr
	}
	if len(groups) == 0 {
		return 0, 0, nil
	}

	// Upsert each group as soon as its own Members fetch completes, rather
	// than batching all groups behind one final upsert — a real (non-
	// permission-denied) error fetching one group's members must only cost
	// that group's row, not every group already listed, mirroring the
	// per-item-commit pattern used by scanLoggingBucketLinks/Views and
	// scanMonitoringSLOs below.
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentMonitoringFanout, groups, func(gctx context.Context, g *monitoring.Group) error {
		if g == nil || g.Name == "" {
			return nil
		}
		var members []*monitoring.MonitoredResource
		memErr := svc.Projects.Groups.Members.List(g.Name).Pages(gctx, func(page *monitoring.ListGroupMembersResponse) error {
			members = append(members, page.Members...)
			return nil
		})
		if memErr != nil {
			if isPermissionDenied(memErr) {
				return skipIfDenied(st, "monitoring:groups.members.list", g.Name, memErr)
			}
			return memErr
		}
		name := g.DisplayName
		batch := []*store.Resource{{
			Provider:       "gcp",
			AccountID:      p.ID,
			AccountName:    &p.Name,
			Type:           TypeMonitoringGroup,
			NativeID:       g.Name,
			Name:           &name,
			AttributesJSON: embedMembersJSON(g, members),
			DiscoveredBy:   scanID,
		}}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// embedMembersJSON merges g's own JSON encoding with a top-level "members"
// key. Struct-embedding *monitoring.Group and marshaling the wrapper
// directly doesn't work: the SDK generates a value-receiver MarshalJSON on
// Group (to handle ForceSendFields), which gets promoted to satisfy
// json.Marshaler on any struct that anonymously embeds *Group — encoding/json
// then calls only that promoted method and silently drops every sibling
// field, Members included. Round-tripping through a map sidesteps the
// promoted-Marshaler trap entirely.
func embedMembersJSON(g *monitoring.Group, members []*monitoring.MonitoredResource) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(mustJSON(g)), &m); err != nil {
		return mustJSON(g)
	}
	if len(members) > 0 {
		m["members"] = json.RawMessage(mustJSON(members))
	}
	return mustJSON(m)
}

// scanMonitoringNotificationChannels discovers notification channels.
// Labels carry per-channel-type config (e.g. Slack webhook URL, PagerDuty
// service key) — redacted, see gcp_redact.go.
func scanMonitoringNotificationChannels(ctx context.Context, svc *monitoring.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	return runPaginated(ctx, st, p, "monitoring:notificationChannels.list",
		svc.Projects.NotificationChannels.List(parent),
		func(page *monitoring.ListNotificationChannelsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.NotificationChannels))
			for _, c := range page.NotificationChannels {
				if c == nil || c.Name == "" {
					continue
				}
				name := c.DisplayName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeMonitoringNotificationChannel,
					NativeID:       c.Name,
					Name:           &name,
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanMonitoringServices discovers Cloud Monitoring Services (the SLO
// grouping concept, unrelated to Cloud Run/GKE services). Returns Service
// NativeIDs for phase's per-Service SLO fan-out.
func scanMonitoringServices(ctx context.Context, svc *monitoring.Service, p *project, st *store.Store, scanID string) (serviceIDs []string, total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	total, inserted, err = runPaginated(ctx, st, p, "monitoring:services.list",
		svc.Services.List(parent),
		func(page *monitoring.ListServicesResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Services))
			for _, s := range page.Services {
				if s == nil || s.Name == "" {
					continue
				}
				serviceIDs = append(serviceIDs, s.Name)
				name := s.DisplayName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeMonitoringService,
					NativeID:       s.Name,
					Name:           &name,
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	return serviceIDs, total, inserted, err
}

// scanMonitoringSLOs fans out ServiceLevelObjectives.List per already-
// scanned Service (no wildcard parent support — requires an explicit
// `services/{service}`).
func scanMonitoringSLOs(ctx context.Context, svc *monitoring.Service, p *project, serviceIDs []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentMonitoringFanout, serviceIDs, func(gctx context.Context, serviceName string) error {
		serviceResID := store.ResourceID("gcp", p.ID, TypeMonitoringService, serviceName)
		var batch []*store.Resource
		listErr := svc.Services.ServiceLevelObjectives.List(serviceName).Pages(gctx, func(page *monitoring.ListServiceLevelObjectivesResponse) error {
			for _, o := range page.ServiceLevelObjectives {
				if o == nil || o.Name == "" {
					continue
				}
				name := o.DisplayName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeMonitoringSLO,
					NativeID:       o.Name,
					Name:           &name,
					AttributesJSON: mustJSON(o),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "monitoring:services.serviceLevelObjectives.list", serviceName, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, serviceResID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanMonitoringSnoozes discovers Snoozes (temporary alert-policy silences).
func scanMonitoringSnoozes(ctx context.Context, svc *monitoring.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	return runPaginated(ctx, st, p, "monitoring:snoozes.list",
		svc.Projects.Snoozes.List(parent),
		func(page *monitoring.ListSnoozesResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Snoozes))
			for _, sn := range page.Snoozes {
				if sn == nil || sn.Name == "" {
					continue
				}
				name := sn.DisplayName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeMonitoringSnooze,
					NativeID:       sn.Name,
					Name:           &name,
					AttributesJSON: mustJSON(sn),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanMonitoringUptimeCheckConfigs discovers Uptime check configurations.
func scanMonitoringUptimeCheckConfigs(ctx context.Context, svc *monitoring.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	return runPaginated(ctx, st, p, "monitoring:uptimeCheckConfigs.list",
		svc.Projects.UptimeCheckConfigs.List(parent),
		func(page *monitoring.ListUptimeCheckConfigsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.UptimeCheckConfigs))
			for _, u := range page.UptimeCheckConfigs {
				if u == nil || u.Name == "" {
					continue
				}
				name := u.DisplayName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeMonitoringUptimeCheckConfig,
					NativeID:       u.Name,
					Name:           &name,
					AttributesJSON: mustJSON(u),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}
