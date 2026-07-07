package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolvePubSubRelationships,
		EdgeDecl{TypePubSubTopic, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypePubSubTopic, TypePubSubSchema, store.RelUses},
		EdgeDecl{TypePubSubSubscription, TypePubSubTopic, store.RelAttachedTo},
		EdgeDecl{TypePubSubSubscription, TypePubSubTopic, store.RelRoutesTo},
	)
}

// resolvePubSubRelationships derives:
//
//   - subscription -[attached-to]-> topic        (subscription.topic)
//   - subscription -[routes-to]-> dead-letter topic (subscription.deadLetterPolicy.deadLetterTopic)
//   - topic        -[uses]-> cryptoKey            (topic.kmsKeyName)
//   - topic        -[uses]-> schema               (topic.schemaSettings.schema)
//
// Push-endpoint URLs (subscription.pushConfig.pushEndpoint) deferred — can
// target Cloud Run / Cloud Functions / arbitrary HTTPS, but rarely matches a
// disco resource NativeID without service-specific hostname parsing.
// BigQuery dataset / Cloud Storage bucket subscription targets deferred —
// BQ scanner is R4.12, Storage edge needs subscription
// `cloudStorageConfig.bucket` parsing alongside.
//
// `_deleted-topic_` strings (Pub/Sub's tombstone for orphaned topic refs)
// won't match the in-store NativeID index, so the FK check skips them
// implicitly.
func resolvePubSubRelationships(p *project, st *store.Store) error {
	topics, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypePubSubTopic},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	topicIDByNative := make(map[string]string, len(topics))
	for _, t := range topics {
		topicIDByNative[t.NativeID] = t.ID
	}

	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeKMSCryptoKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	keyIDByNative := make(map[string]string, len(keys))
	for _, k := range keys {
		keyIDByNative[k.NativeID] = k.ID
	}

	schemas, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypePubSubSchema},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	schemaIDByNative := make(map[string]string, len(schemas))
	for _, s := range schemas {
		schemaIDByNative[s.NativeID] = s.ID
	}

	// Topic → cryptoKey + schema.
	for _, t := range topics {
		var a struct {
			KmsKeyName     string `json:"kmsKeyName"`
			SchemaSettings struct {
				Schema string `json:"schema"`
			} `json:"schemaSettings"`
		}
		if err := json.Unmarshal([]byte(t.AttributesJSON), &a); err != nil {
			continue
		}
		if a.KmsKeyName != "" {
			if keyID, ok := keyIDByNative[stripCryptoKeyVersion(a.KmsKeyName)]; ok {
				if err := st.UpsertRelationship(t.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert topic→cryptoKey: %w", err)
				}
			}
		}
		if a.SchemaSettings.Schema != "" {
			if schemaID, ok := schemaIDByNative[a.SchemaSettings.Schema]; ok {
				if err := st.UpsertRelationship(t.ID, schemaID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert topic→schema: %w", err)
				}
			}
		}
	}

	// Subscription → topic + dead-letter.
	subs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypePubSubSubscription},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, s := range subs {
		var a struct {
			Topic            string `json:"topic"`
			DeadLetterPolicy struct {
				DeadLetterTopic string `json:"deadLetterTopic"`
			} `json:"deadLetterPolicy"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &a); err != nil {
			continue
		}
		if topicID, ok := topicIDByNative[a.Topic]; ok {
			if err := st.UpsertRelationship(s.ID, topicID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subscription→topic: %w", err)
			}
		}
		if a.DeadLetterPolicy.DeadLetterTopic != "" {
			if dlID, ok := topicIDByNative[a.DeadLetterPolicy.DeadLetterTopic]; ok {
				if err := st.UpsertRelationship(s.ID, dlID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert subscription→DLQ: %w", err)
				}
			}
		}
	}
	return nil
}
