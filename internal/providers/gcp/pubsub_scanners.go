package gcp

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/pubsub/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypePubSubTopic, Service: "pubsub", Upstream: "pubsub.googleapis.com/Topic"})
	registerType(restype.Descriptor{Type: TypePubSubSubscription, Service: "pubsub", Upstream: "pubsub.googleapis.com/Subscription"})
	registerType(restype.Descriptor{Type: TypePubSubSchema, Service: "pubsub", Upstream: "pubsub.googleapis.com/Schema", Leaf: true})
	registerType(restype.Descriptor{Type: TypePubSubSnapshot, Service: "pubsub", Upstream: "pubsub.googleapis.com/Snapshot"})
	registerService(serviceEntry{
		name: "gcp:pubsub",
		fn:   scanPubSub,
	})
}

// scanPubSub discovers Pub/Sub topics, subscriptions, schemas, and
// snapshots. All four list APIs are project-scoped (no per-location fan-out
// — Pub/Sub's API surface is global even though message storage is
// regional).
func scanPubSub(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := pubsub.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("pubsub client: %w", err)
	}
	return scanPubSubWithClient(ctx, svc, p, st, scanID)
}

// scanPubSubWithClient is the test seam for scanPubSub — takes the
// pre-built client directly so tests can point it at a fake server.
func scanPubSubWithClient(ctx context.Context, svc *pubsub.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
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

	// Subscriptions, Schemas, and Snapshots are all nested after Topics
	// (above) already proved the pubsub API enabled for this project —
	// classify each once via a manual Pages() call and discard rather than
	// escalate an isAPINotEnabled-shaped error to the whole-service disabled
	// sentinel.
	suberr := svc.Projects.Subscriptions.List(parent).Pages(ctx, func(page *pubsub.ListSubscriptionsResponse) error {
		batch := make([]*store.Resource, 0, len(page.Subscriptions))
		for _, s := range page.Subscriptions {
			if s == nil || s.Name == "" {
				continue
			}
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
		st2, sn2, uerr := upsertWithProjClosure(p, st, batch)
		total += st2
		inserted += sn2
		return uerr
	})
	if suberr != nil {
		if isPermissionDenied(suberr) {
			_ = skipIfDenied(st, "pubsub:subscriptions.list", p.ID, suberr)
		} else {
			return total, inserted, suberr
		}
	}

	scherr := svc.Projects.Schemas.List(parent).Pages(ctx, func(page *pubsub.ListSchemasResponse) error {
		batch := make([]*store.Resource, 0, len(page.Schemas))
		for _, s := range page.Schemas {
			if s == nil || s.Name == "" {
				continue
			}
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
		scht, schn, uerr := upsertWithProjClosure(p, st, batch)
		total += scht
		inserted += schn
		return uerr
	})
	if scherr != nil {
		if isPermissionDenied(scherr) {
			_ = skipIfDenied(st, "pubsub:schemas.list", p.ID, scherr)
		} else {
			return total, inserted, scherr
		}
	}

	snaperr := svc.Projects.Snapshots.List(parent).Pages(ctx, func(page *pubsub.ListSnapshotsResponse) error {
		batch := make([]*store.Resource, 0, len(page.Snapshots))
		for _, s := range page.Snapshots {
			if s == nil || s.Name == "" {
				continue
			}
			name := lastSegment(s.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypePubSubSnapshot,
				NativeID:       s.Name,
				Name:           &name,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		snapt, snapn, uerr := upsertWithProjClosure(p, st, batch)
		total += snapt
		inserted += snapn
		return uerr
	})
	if snaperr != nil {
		if isPermissionDenied(snaperr) {
			_ = skipIfDenied(st, "pubsub:snapshots.list", p.ID, snaperr)
		} else {
			return total, inserted, snaperr
		}
	}
	return total, inserted, nil
}
