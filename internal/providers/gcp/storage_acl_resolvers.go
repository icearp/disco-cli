package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R10 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): Cloud Storage's per-bucket child types. HmacKey's
// owning service account, Notification's Pub/Sub destination, and both ACL
// types' service-account entity edge — all read straight off already-scanned
// AttributesJSON.
func init() {
	registerResolver(resolveStorageHmacKeyRelationships,
		EdgeDecl{TypeStorageHmacKey, TypeIAMServiceAccount, store.RelUses},
	)
	registerResolver(resolveStorageNotificationRelationships,
		EdgeDecl{TypeStorageNotification, TypePubSubTopic, store.RelRoutesTo},
	)
	registerResolver(resolveStorageAccessControlRelationships,
		EdgeDecl{TypeStorageBucketAccessControl, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeStorageDefaultObjectAccessControl, TypeIAMServiceAccount, store.RelUses},
	)
}

// resolveStorageHmacKeyRelationships wires HmacKey -> the IAM ServiceAccount
// it authenticates as (`serviceAccountEmail`).
func resolveStorageHmacKeyRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeStorageHmacKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	if len(saByEmail) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			ServiceAccountEmail string `json:"serviceAccountEmail"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		saID, ok := saByEmail[attrs.ServiceAccountEmail]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, saID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert hmacKey→serviceAccount: %w", err)
		}
	}
	return nil
}

// resolveStorageNotificationRelationships wires Notification -> the Pub/Sub
// Topic it publishes to (`topic`, formatted as
// "//pubsub.googleapis.com/projects/{p}/topics/{name}" per the GCS API's own
// doc comment — the "//pubsub.googleapis.com/" prefix is stripped to match
// PubSubTopic's NativeID, which is the bare Pub/Sub resource name
// "projects/{p}/topics/{name}").
func resolveStorageNotificationRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeStorageNotification},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	topics, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypePubSubTopic},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(topics) == 0 {
		return nil
	}
	topicIDByNative := make(map[string]string, len(topics))
	for _, t := range topics {
		topicIDByNative[t.NativeID] = t.ID
	}
	for _, r := range rows {
		var attrs struct {
			Topic string `json:"topic"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		topicName := strings.TrimPrefix(attrs.Topic, "//pubsub.googleapis.com/")
		topicID, ok := topicIDByNative[topicName]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, topicID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert notification→pubsubTopic: %w", err)
		}
	}
	return nil
}

// resolveStorageAccessControlRelationships wires BucketAccessControl and
// DefaultObjectAccessControl -> the IAM ServiceAccount named by their
// `email` field, when the ACL entity happens to be a service account (the
// field is also populated for user/group/domain entities and empty for
// allUsers/allAuthenticatedUsers/project-team entities — buildSAEmailIndex
// simply misses those, no special-casing needed).
func resolveStorageAccessControlRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeStorageBucketAccessControl, TypeStorageDefaultObjectAccessControl},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	if len(saByEmail) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			Email string `json:"email"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Email == "" {
			continue
		}
		saID, ok := saByEmail[attrs.Email]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, saID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert accessControl→serviceAccount: %w", err)
		}
	}
	return nil
}
