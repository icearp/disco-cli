package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveLoggingSinkRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	bucketID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageBucket,
		"https://www.googleapis.com/storage/v1/b/log-bucket", "us", "{}")
	dsID := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset,
		"my-project:logs", "us", "{}")
	topicID := upsertTestResource(t, st, "gcp", p.ID, TypePubSubTopic,
		"projects/my-project/topics/log-topic", "", "{}")

	storageSink := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingSink,
		"projects/my-project/sinks/to-storage", "",
		`{"destination": "storage.googleapis.com/log-bucket"}`)
	bqSink := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingSink,
		"projects/my-project/sinks/to-bq", "",
		`{"destination": "bigquery.googleapis.com/projects/my-project/datasets/logs"}`)
	pubSubSink := upsertTestResource(t, st, "gcp", p.ID, TypeLoggingSink,
		"projects/my-project/sinks/to-pubsub", "",
		`{"destination": "pubsub.googleapis.com/projects/my-project/topics/log-topic"}`)
	// Unknown destination — must not error.
	upsertTestResource(t, st, "gcp", p.ID, TypeLoggingSink,
		"projects/my-project/sinks/orphan", "",
		`{"destination": "logging.googleapis.com/projects/my-project/locations/us/buckets/_Default"}`)

	if err := resolveLoggingSinkRelationships(p, st); err != nil {
		t.Fatalf("resolveLoggingSinkRelationships: %v", err)
	}

	check := func(label, fromID, want string) {
		t.Helper()
		rels, _ := st.RelationshipsFrom(fromID)
		if len(rels) != 1 || rels[0].ToID != want || rels[0].Kind != store.RelRoutesTo {
			t.Errorf("%s: got %+v, want →%s routes-to", label, rels, want)
		}
	}
	check("storage", storageSink, bucketID)
	check("bigquery", bqSink, dsID)
	check("pubsub", pubSubSink, topicID)
}
