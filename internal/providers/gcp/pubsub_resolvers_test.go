package gcp

import (
	"testing"

	"github.com/icearp/disco-cli/store"
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

	snapID := upsertTestResource(t, st, "gcp", p.ID, TypePubSubSnapshot,
		"projects/my-project/snapshots/snap1", "", `{"topic": "`+topicName+`"}`)

	if err := resolvePubSubRelationships(p, st); err != nil {
		t.Fatalf("resolvePubSubRelationships: %v", err)
	}

	relsSnap, _ := st.RelationshipsFrom(snapID)
	if len(relsSnap) != 1 || relsSnap[0].ToID != topicID || relsSnap[0].Kind != store.RelUses {
		t.Errorf("snapshot edges: got %+v, want →topic uses", relsSnap)
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

func TestResolvePubSubRelationships_SnapshotUnscannedTopicSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	snapID := upsertTestResource(t, st, "gcp", p.ID, TypePubSubSnapshot,
		"projects/my-project/snapshots/snap1", "", `{"topic": "_deleted-topic_"}`)

	if err := resolvePubSubRelationships(p, st); err != nil {
		t.Fatalf("resolvePubSubRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(snapID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for a deleted/unscanned topic, got %+v", rels)
	}
}

func TestResolvePubSubRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolvePubSubRelationships(p, st); err != nil {
		t.Fatalf("resolvePubSubRelationships on empty project: %v", err)
	}
}
