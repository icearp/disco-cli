package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveDatabasesRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us-central1/keyRings/r/cryptoKeys/k"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us-central1", "{}")

	btID := upsertTestResource(t, st, "gcp", p.ID, TypeBigtableCluster,
		"projects/my-project/instances/i1/clusters/c1", "us-central1",
		`{"encryptionConfig": {"kmsKeyName": "`+keyName+`"}}`)
	fsID := upsertTestResource(t, st, "gcp", p.ID, TypeFirestoreDB,
		"projects/my-project/databases/(default)", "us-central1",
		`{"cmekConfig": {"kmsKeyName": "`+keyName+`"}}`)
	spID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerDatabase,
		"projects/my-project/instances/i1/databases/d1", "us-central1",
		`{"encryptionConfig": {"kmsKeyName": "`+keyName+`"}}`)

	if err := resolveDatabasesRelationships(p, st); err != nil {
		t.Fatalf("resolveDatabasesRelationships: %v", err)
	}

	for label, fromID := range map[string]string{"bigtable": btID, "firestore": fsID, "spanner": spID} {
		rels, _ := st.RelationshipsFrom(fromID)
		if len(rels) != 1 || rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
			t.Errorf("%s: got %+v, want →key uses", label, rels)
		}
	}
}
