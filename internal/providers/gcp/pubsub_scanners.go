package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/pubsub/v1"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:pubsub",
		fn:   scanPubSub,
		emits: []coverage.TypeDecl{
			{Service: "pubsub", DiscoType: TypePubSubTopic},
			{Service: "pubsub", DiscoType: TypePubSubSubscription},
			{Service: "pubsub", DiscoType: TypePubSubSchema},
		},
	})
}

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
	t, n, err := runPaginated(ctx, st, p, "pubsub:topics.list",
		svc.Projects.Topics.List(parent),
		func(page *pubsub.ListTopicsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Topics))
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
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Subscriptions.
	t, n, err = runPaginated(ctx, st, p, "pubsub:subscriptions.list",
		svc.Projects.Subscriptions.List(parent),
		func(page *pubsub.ListSubscriptionsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Subscriptions))
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
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Schemas.
	t, n, err = runPaginated(ctx, st, p, "pubsub:schemas.list",
		svc.Projects.Schemas.List(parent),
		func(page *pubsub.ListSchemasResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Schemas))
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
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	return total, inserted, err
}
