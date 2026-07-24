package gcp

import (
	"testing"

	"github.com/icearp/disco-cli/store"
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
	sbsID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerBackupSchedule,
		"projects/my-project/instances/i1/databases/d1/backupSchedules/bs1", "us-central1",
		`{"encryptionConfig": {"kmsKeyName": "`+keyName+`"}}`)

	if err := resolveDatabasesRelationships(p, st); err != nil {
		t.Fatalf("resolveDatabasesRelationships: %v", err)
	}

	for label, fromID := range map[string]string{"bigtable": btID, "firestore": fsID, "spanner": spID, "spanner-backup-schedule": sbsID} {
		rels, _ := st.RelationshipsFrom(fromID)
		if len(rels) != 1 || rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
			t.Errorf("%s: got %+v, want →key uses", label, rels)
		}
	}
}

func TestResolveDatabasesRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveDatabasesRelationships(p, st); err != nil {
		t.Fatalf("resolveDatabasesRelationships on empty project: %v", err)
	}
}

func TestResolveDatabasesRelationships_SpannerBackupScheduleNoCMEKNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	sbsID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerBackupSchedule,
		"projects/my-project/instances/i1/databases/d1/backupSchedules/bs1", "us-central1", "{}")

	if err := resolveDatabasesRelationships(p, st); err != nil {
		t.Fatalf("resolveDatabasesRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(sbsID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge when encryptionConfig is unset, got %+v", rels)
	}
}

// TestResolveDatabasesRelationships_SpannerMultiRegionKmsKeyNames covers the
// multi-region CMEK form (encryptionConfig.kmsKeyNames[], one key per
// covered region) alongside the single-key kmsKeyName form the base test
// already exercises.
func TestResolveDatabasesRelationships_SpannerMultiRegionKmsKeyNames(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	key1Name := "projects/my-project/locations/us-central1/keyRings/r/cryptoKeys/k1"
	key1ID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, key1Name, "us-central1", "{}")
	key2Name := "projects/my-project/locations/us-east1/keyRings/r/cryptoKeys/k2"
	key2ID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, key2Name, "us-east1", "{}")

	spID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerDatabase,
		"projects/my-project/instances/i1/databases/d1", "us-central1",
		`{"encryptionConfig": {"kmsKeyNames": ["`+key1Name+`", "`+key2Name+`"]}}`)

	if err := resolveDatabasesRelationships(p, st); err != nil {
		t.Fatalf("resolveDatabasesRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(spID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges (one per region key), got %d: %+v", len(rels), rels)
	}
	want := map[string]bool{key1ID: false, key2ID: false}
	for _, r := range rels {
		if _, ok := want[r.ToID]; !ok {
			t.Errorf("unexpected edge target %q", r.ToID)
			continue
		}
		want[r.ToID] = true
	}
	for id, hit := range want {
		if !hit {
			t.Errorf("missing edge to %q", id)
		}
	}
}
