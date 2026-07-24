package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveEventGridRelationships,
		// event-subscription -[attached-to]-> topic / system-topic / domain (properties.topic).
		EdgeDecl{Source: TypeEventGridEventSubscription, Target: TypeEventGridTopic, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeEventGridEventSubscription, Target: TypeEventGridSystemTopic, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeEventGridEventSubscription, Target: TypeEventGridDomain, Kind: store.RelAttachedTo},
		// event-subscription -[uses]-> destination + dead-letter resource families
		// (function / storage queue+account / service bus / event hub / relay hybrid connection).
		// Webhook/URL destinations carry no ARM ID and are skipped.
		EdgeDecl{Source: TypeEventGridEventSubscription, Target: TypeAppServiceSite, Kind: store.RelUses},
		EdgeDecl{Source: TypeEventGridEventSubscription, Target: TypeStorageStorageAccount, Kind: store.RelUses},
		EdgeDecl{Source: TypeEventGridEventSubscription, Target: TypeServiceBusNamespace, Kind: store.RelUses},
		EdgeDecl{Source: TypeEventGridEventSubscription, Target: TypeEventHubNamespace, Kind: store.RelUses},
		EdgeDecl{Source: TypeEventGridEventSubscription, Target: TypeRelayNamespace, Kind: store.RelUses},
		// system-topic -[uses]-> source resource (properties.source); storage is the canonical source.
		EdgeDecl{Source: TypeEventGridSystemTopic, Target: TypeStorageStorageAccount, Kind: store.RelUses},
	)
}

// eventGridResourceIDProps is the shared `properties:{resourceId}` shape
// used by EventGrid destination + dead-letter destination payloads.
type eventGridResourceIDProps struct {
	Properties *struct {
		ResourceID *string `json:"resourceId"`
	} `json:"properties"`
}

type eventGridSubscriptionAttrs struct {
	Properties *struct {
		Topic       *string `json:"topic"`
		Destination *struct {
			EndpointType *string `json:"endpointType"`
			Properties   *struct {
				ResourceID *string `json:"resourceId"`
			} `json:"properties"`
		} `json:"destination"`
		DeadLetterDestination          *eventGridResourceIDProps `json:"deadLetterDestination"`
		DeadLetterWithResourceIdentity *struct {
			DeadLetterDestination *eventGridResourceIDProps `json:"deadLetterDestination"`
		} `json:"deadLetterWithResourceIdentity"`
	} `json:"properties"`
}

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
// the namespace ARM ID for `azure:microsoft.servicebus:namespaces`, so the
// resolver progressively trims `/`-segments from the right (precedent: PE
// resolver) until a stored resource matches.
func resolveEventGridRelationships(sub *subscription, st *store.Store) error {
	subscriptions, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeEventGridEventSubscription},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	systemTopics, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeEventGridSystemTopic},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(subscriptions) == 0 && len(systemTopics) == 0 {
		return nil
	}

	idx, err := loadAzureNativeIDIndex(sub, st)
	if err != nil {
		return err
	}
	for _, r := range subscriptions {
		if err := emitEventGridSubscriptionEdges(st, r, idx); err != nil {
			return err
		}
	}
	for _, r := range systemTopics {
		if err := emitEventGridSystemTopicEdges(st, r, idx); err != nil {
			return err
		}
	}
	return nil
}

// loadAzureNativeIDIndex builds the per-sub case-insensitive NativeID →
// disco-ID lookup used by EventGrid + similar progressive-trim resolvers.
func loadAzureNativeIDIndex(sub *subscription, st *store.Store) (map[string]string, error) {
	all, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(all))
	for _, r := range all {
		idx[strings.ToLower(r.NativeID)] = r.ID
	}
	return idx, nil
}

// resolveARMIDProgressive trims trailing `/`-segments off armID until the
// case-insensitive NativeID index returns a hit (e.g. SB topic ARM ID
// resolves to its parent SB namespace).
func resolveARMIDProgressive(armID string, idx map[string]string) string {
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

func emitEventGridSubscriptionEdges(st *store.Store, r store.Resource, idx map[string]string) error {
	var attrs eventGridSubscriptionAttrs
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
		return nil
	}
	p := attrs.Properties
	if p.Topic != nil {
		if toID := resolveARMIDProgressive(*p.Topic, idx); toID != "" && toID != r.ID {
			if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eventgrid sub→topic: %w", err)
			}
		}
	}
	if p.Destination != nil && p.Destination.Properties != nil && p.Destination.Properties.ResourceID != nil {
		if toID := resolveARMIDProgressive(*p.Destination.Properties.ResourceID, idx); toID != "" && toID != r.ID {
			if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert eventgrid sub→destination: %w", err)
			}
		}
	}
	dlqID := pickEventGridDLQID(p.DeadLetterDestination, p.DeadLetterWithResourceIdentity)
	if dlqID != "" {
		if toID := resolveARMIDProgressive(dlqID, idx); toID != "" && toID != r.ID {
			if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert eventgrid sub→dlq: %w", err)
			}
		}
	}
	return nil
}

func pickEventGridDLQID(dld *eventGridResourceIDProps, dlqWRI *struct {
	DeadLetterDestination *eventGridResourceIDProps `json:"deadLetterDestination"`
},
) string {
	if dld != nil && dld.Properties != nil && dld.Properties.ResourceID != nil {
		return *dld.Properties.ResourceID
	}
	if dlqWRI != nil && dlqWRI.DeadLetterDestination != nil && dlqWRI.DeadLetterDestination.Properties != nil && dlqWRI.DeadLetterDestination.Properties.ResourceID != nil {
		return *dlqWRI.DeadLetterDestination.Properties.ResourceID
	}
	return ""
}

func emitEventGridSystemTopicEdges(st *store.Store, r store.Resource, idx map[string]string) error {
	var attrs struct {
		Properties *struct {
			Source *string `json:"source"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.Source == nil {
		return nil
	}
	if toID := resolveARMIDProgressive(*attrs.Properties.Source, idx); toID != "" && toID != r.ID {
		if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert eventgrid system-topic→source: %w", err)
		}
	}
	return nil
}
