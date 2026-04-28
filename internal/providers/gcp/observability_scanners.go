package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/logging/v2"
	"google.golang.org/api/monitoring/v3"
)

func init() {
	registerService(serviceEntry{name: "gcp:logging", fn: scanLoggingSinks})
	registerService(serviceEntry{name: "gcp:monitoring", fn: scanMonitoringAlertPolicies})
}

// scanLoggingSinks discovers Cloud Logging sinks. Folder + organization
// scope sinks are deferred — they're scoped above the per-project fan-out
// shape. Per-bucket views and exclusion filters deferred (low graph value).
func scanLoggingSinks(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := logging.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("logging client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s", p.ID)
	err = svc.Projects.Sinks.List(parent).Pages(ctx, func(page *logging.ListSinksResponse) error {
		var batch []*store.Resource
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
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "logging:sinks.list", p.ID, err)
		}
		return 0, 0, err
	}
	return total, inserted, nil
}

// scanMonitoringAlertPolicies discovers Cloud Monitoring alert policies.
// Notification channels (referenced by alert.notificationChannels[]) are not
// scanned this iteration — they'd be a sibling Channels.List call; deferred
// to alert-policy → channel resolver follow-up.
func scanMonitoringAlertPolicies(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := monitoring.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("monitoring client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s", p.ID)
	err = svc.Projects.AlertPolicies.List(parent).Pages(ctx, func(page *monitoring.ListAlertPoliciesResponse) error {
		var batch []*store.Resource
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
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	})
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "monitoring:alertPolicies.list", p.ID, err)
		}
		return 0, 0, err
	}
	return total, inserted, nil
}
