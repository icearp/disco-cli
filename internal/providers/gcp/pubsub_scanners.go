package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/pubsub/v1"
)

func init() { registerService(serviceEntry{name: "gcp:pubsub", fn: scanPubSub}) }

// scanPubSub discovers Pub/Sub topics, subscriptions, and schemas. All three
// list APIs are project-scoped (no per-location fan-out — Pub/Sub is global
// at the API surface even though messages are stored regionally).
func scanPubSub(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := pubsub.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("pubsub client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s", p.ID)

	// Topics.
	if err := svc.Projects.Topics.List(parent).Pages(ctx, func(page *pubsub.ListTopicsResponse) error {
		var batch []*store.Resource
		for _, t := range page.Topics {
			name := lastSegment(t.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypePubSubTopic,
				NativeID:       t.Name,
				Name:           &name,
				Status:         strp(t.State),
				AttributesJSON: mustJSON(t),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "pubsub:topics.list", p.ID, err)
		}
		return 0, 0, err
	}

	// Subscriptions.
	if err := svc.Projects.Subscriptions.List(parent).Pages(ctx, func(page *pubsub.ListSubscriptionsResponse) error {
		var batch []*store.Resource
		for _, s := range page.Subscriptions {
			name := lastSegment(s.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypePubSubSubscription,
				NativeID:       s.Name,
				Name:           &name,
				Status:         strp(s.State),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	}); err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "pubsub:subscriptions.list", p.ID, err)
		}
		return total, inserted, err
	}

	// Schemas.
	if err := svc.Projects.Schemas.List(parent).Pages(ctx, func(page *pubsub.ListSchemasResponse) error {
		var batch []*store.Resource
		for _, s := range page.Schemas {
			name := lastSegment(s.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypePubSubSchema,
				NativeID:       s.Name,
				Name:           &name,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	}); err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "pubsub:schemas.list", p.ID, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}
