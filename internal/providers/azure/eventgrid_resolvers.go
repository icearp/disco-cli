package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveEventGridRelationships) }

// resolveEventGridRelationships derives Event Grid edges:
//   - event-subscription -[attached-to]-> topic / system-topic / domain (per
//     `properties.topic` ARM ID).
//   - event-subscription -[uses]-> destination resource (Function / Storage
//     Queue / Service Bus Queue / Service Bus Topic / Event Hub / Hybrid
//     Connection) via `properties.destination.properties.resourceId`. Webhook
//     destinations carry no ARM ID and are skipped silently.
//   - event-subscription -[uses]-> dead-letter destination (Storage account)
//     via `properties.deadLetterDestination.properties.resourceId`. Same shape
//     for `deadLetterWithResourceIdentity.deadLetterDestination`.
//   - system-topic -[uses]-> source resource via `properties.source` (the ARM
//     ID of the resource generating events — provider-agnostic FK against the
//     per-sub NativeID index).
//
// Service Bus topic destination resourceIds carry the topic suffix (e.g.
// `…/namespaces/foo/topics/bar`); the per-sub NativeID index already keys on
// the namespace ARM ID for `azure:microsoft.servicebus:namespace`, so the
// resolver progressively trims `/`-segments from the right (precedent: PE
// resolver) until a stored resource matches.
func resolveEventGridRelationships(sub *subscription, st *store.Store) error {
	subscriptions, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeEventGridEventSubscription},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	systemTopics, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeEventGridSystemTopic},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(subscriptions) == 0 && len(systemTopics) == 0 {
		return nil
	}

	// Per-sub case-insensitive NativeID → disco-ID index across every Azure
	// resource. Same pattern as PE / RBAC resolvers.
	all, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	idx := make(map[string]string, len(all))
	for _, r := range all {
		idx[strings.ToLower(r.NativeID)] = r.ID
	}

	// Helper: lookup ARM ID with progressive trim of trailing /-segments to
	// recover a stored parent resource (e.g. SB topic → SB namespace).
	resolve := func(armID string) string {
		if armID == "" {
			return ""
		}
		key := strings.ToLower(armID)
		for {
			if id, ok := idx[key]; ok {
				return id
			}
			i := strings.LastIndex(key, "/")
			if i <= 0 {
				return ""
			}
			key = key[:i]
		}
	}

	for _, r := range subscriptions {
		var attrs struct {
			Properties *struct {
				Topic       *string `json:"topic"`
				Destination *struct {
					EndpointType *string `json:"endpointType"`
					Properties   *struct {
						ResourceID *string `json:"resourceId"`
					} `json:"properties"`
				} `json:"destination"`
				DeadLetterDestination *struct {
					Properties *struct {
						ResourceID *string `json:"resourceId"`
					} `json:"properties"`
				} `json:"deadLetterDestination"`
				DeadLetterWithResourceIdentity *struct {
					DeadLetterDestination *struct {
						Properties *struct {
							ResourceID *string `json:"resourceId"`
						} `json:"properties"`
					} `json:"deadLetterDestination"`
				} `json:"deadLetterWithResourceIdentity"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		p := attrs.Properties
		if p.Topic != nil {
			if toID := resolve(*p.Topic); toID != "" && toID != r.ID {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert eventgrid sub→topic: %w", err)
				}
			}
		}
		if p.Destination != nil && p.Destination.Properties != nil && p.Destination.Properties.ResourceID != nil {
			if toID := resolve(*p.Destination.Properties.ResourceID); toID != "" && toID != r.ID {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert eventgrid sub→destination: %w", err)
				}
			}
		}
		var dlqID *string
		if p.DeadLetterDestination != nil && p.DeadLetterDestination.Properties != nil {
			dlqID = p.DeadLetterDestination.Properties.ResourceID
		} else if p.DeadLetterWithResourceIdentity != nil && p.DeadLetterWithResourceIdentity.DeadLetterDestination != nil && p.DeadLetterWithResourceIdentity.DeadLetterDestination.Properties != nil {
			dlqID = p.DeadLetterWithResourceIdentity.DeadLetterDestination.Properties.ResourceID
		}
		if dlqID != nil {
			if toID := resolve(*dlqID); toID != "" && toID != r.ID {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert eventgrid sub→dlq: %w", err)
				}
			}
		}
	}

	for _, r := range systemTopics {
		var attrs struct {
			Properties *struct {
				Source *string `json:"source"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.Source == nil {
			continue
		}
		if toID := resolve(*attrs.Properties.Source); toID != "" && toID != r.ID {
			if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert eventgrid system-topic→source: %w", err)
			}
		}
	}
	return nil
}
