package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolvePubSubRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us-central1/keyRings/r/cryptoKeys/k"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us-central1", "{}")

	schemaName := "projects/my-project/schemas/sch1"
	schemaID := upsertTestResource(t, st, "gcp", p.ID, TypePubSubSchema, schemaName, "", "{}")

	topicName := "projects/my-project/topics/t1"
	dlqName := "projects/my-project/topics/dlq"
	topicID := upsertTestResource(t, st, "gcp", p.ID, TypePubSubTopic, topicName, "",
		`{"kmsKeyName": "`+keyName+`", "schemaSettings": {"schema": "`+schemaName+`"}}`)
	dlqID := upsertTestResource(t, st, "gcp", p.ID, TypePubSubTopic, dlqName, "", "{}")

	subName := "projects/my-project/subscriptions/s1"
	subID := upsertTestResource(t, st, "gcp", p.ID, TypePubSubSubscription, subName, "",
		`{"topic": "`+topicName+`", "deadLetterPolicy": {"deadLetterTopic": "`+dlqName+`"}}`)

	// Subscription pointing at deleted topic — must skip both edges, not error.
	upsertTestResource(t, st, "gcp", p.ID, TypePubSubSubscription,
		"projects/my-project/subscriptions/orphan", "", `{"topic": "_deleted-topic_"}`)

	if err := resolvePubSubRelationships(p, st); err != nil {
		t.Fatalf("resolvePubSubRelationships: %v", err)
	}

	relsTopic, _ := st.RelationshipsFrom(topicID)
	got := map[string]string{}
	for _, r := range relsTopic {
		got[r.ToID] = r.Kind
	}
	if got[keyID] != store.RelUses || got[schemaID] != store.RelUses {
		t.Errorf("topic edges: got %+v, want →key + →schema", got)
	}

	relsSub, _ := st.RelationshipsFrom(subID)
	got = map[string]string{}
	for _, r := range relsSub {
		got[r.ToID] = r.Kind
	}
	if got[topicID] != store.RelAttachedTo || got[dlqID] != store.RelRoutesTo {
		t.Errorf("sub edges: got %+v, want →topic (attached-to) + →dlq (routes-to)", got)
	}
}
