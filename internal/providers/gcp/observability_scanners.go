package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/logging/v2"
	"google.golang.org/api/monitoring/v3"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:logging",
		fn:   scanLoggingSinks,
		emits: []coverage.TypeDecl{
			{Service: "logging", DiscoType: TypeLoggingSink},
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
