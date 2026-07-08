package gcp

import (
	"testing"

	kms "google.golang.org/api/cloudkms/v1"
	spanner "google.golang.org/api/spanner/v1"
)

func TestResolveSpannerInstanceRelationships_ToInstanceConfig(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	icID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstanceConfig, "projects/proj-1/instanceConfigs/regional-us-central1", "",
		marshalAttrs(t, &spanner.InstanceConfig{Name: "projects/proj-1/instanceConfigs/regional-us-central1"}))

	instID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstance, "projects/proj-1/instances/inst-1", "",
		marshalAttrs(t, &spanner.Instance{
			Name:   "projects/proj-1/instances/inst-1",
			Config: "projects/proj-1/instanceConfigs/regional-us-central1",
		}))

	if err := resolveSpannerInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != icID || rels[0].Kind != "uses" {
		t.Errorf("want instance->instanceConfig edge, got %+v", rels)
	}
}

func TestResolveSpannerInstanceRelationships_UnscannedConfigSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	instID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstance, "projects/proj-1/instances/inst-1", "",
		marshalAttrs(t, &spanner.Instance{
			Name:   "projects/proj-1/instances/inst-1",
			Config: "projects/proj-1/instanceConfigs/not-scanned",
		}))

	if err := resolveSpannerInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned instanceConfig, got %+v", rels)
	}
}

func TestResolveSpannerInstanceRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveSpannerInstanceRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerInstanceRelationships on empty project: %v", err)
	}
}

func TestResolveSpannerInstanceConfigRelationships_ToBaseConfig(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	baseID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstanceConfig, "projects/proj-1/instanceConfigs/nam3", "",
		marshalAttrs(t, &spanner.InstanceConfig{Name: "projects/proj-1/instanceConfigs/nam3", ConfigType: "GOOGLE_MANAGED"}))

	customID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstanceConfig, "projects/proj-1/instanceConfigs/custom-1", "",
		marshalAttrs(t, &spanner.InstanceConfig{
			Name:       "projects/proj-1/instanceConfigs/custom-1",
			ConfigType: "USER_MANAGED",
			BaseConfig: "projects/proj-1/instanceConfigs/nam3",
		}))

	if err := resolveSpannerInstanceConfigRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerInstanceConfigRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(customID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != baseID || rels[0].Kind != "uses" {
		t.Errorf("want custom->base instanceConfig edge, got %+v", rels)
	}
}

func TestResolveSpannerInstanceConfigRelationships_GoogleManagedNoBaseConfigSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	icID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstanceConfig, "projects/proj-1/instanceConfigs/nam3", "",
		marshalAttrs(t, &spanner.InstanceConfig{Name: "projects/proj-1/instanceConfigs/nam3", ConfigType: "GOOGLE_MANAGED"}))

	if err := resolveSpannerInstanceConfigRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerInstanceConfigRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(icID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for Google-managed config with empty baseConfig, got %+v", rels)
	}
}

func TestResolveSpannerInstanceConfigRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveSpannerInstanceConfigRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerInstanceConfigRelationships on empty project: %v", err)
	}
}

func TestResolveSpannerInstancePartitionRelationships_ToInstanceConfig(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	icID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstanceConfig, "projects/proj-1/instanceConfigs/regional-us-central1", "",
		marshalAttrs(t, &spanner.InstanceConfig{Name: "projects/proj-1/instanceConfigs/regional-us-central1"}))

	partID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerInstancePartition, "projects/proj-1/instances/inst-1/instancePartitions/part-1", "",
		marshalAttrs(t, &spanner.InstancePartition{
			Name:   "projects/proj-1/instances/inst-1/instancePartitions/part-1",
			Config: "projects/proj-1/instanceConfigs/regional-us-central1",
		}))

	if err := resolveSpannerInstancePartitionRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerInstancePartitionRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(partID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != icID || rels[0].Kind != "uses" {
		t.Errorf("want instancePartition->instanceConfig edge, got %+v", rels)
	}
}

func TestResolveSpannerInstancePartitionRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveSpannerInstancePartitionRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerInstancePartitionRelationships on empty project: %v", err)
	}
}

func TestResolveSpannerBackupRelationships_ToDatabaseAndCryptoKey(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	dbID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerDatabase, "projects/proj-1/instances/inst-1/databases/db-1", "",
		marshalAttrs(t, &spanner.Database{Name: "projects/proj-1/instances/inst-1/databases/db-1"}))

	keyNative := "projects/proj-1/locations/us-central1/keyRings/ring-1/cryptoKeys/key-1"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyNative, "us-central1",
		marshalAttrs(t, &kms.CryptoKey{Name: keyNative}))

	backupID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerBackup, "projects/proj-1/instances/inst-1/backups/backup-1", "",
		marshalAttrs(t, &spanner.Backup{
			Name:     "projects/proj-1/instances/inst-1/backups/backup-1",
			Database: "projects/proj-1/instances/inst-1/databases/db-1",
			EncryptionInfo: &spanner.EncryptionInfo{
				KmsKeyVersion: keyNative + "/cryptoKeyVersions/1",
			},
		}))

	if err := resolveSpannerBackupRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerBackupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(backupID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges, got %d: %+v", len(rels), rels)
	}
	want := map[string]bool{dbID: false, keyID: false}
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

func TestResolveSpannerBackupRelationships_NoEncryptionInfoOnlyDatabaseEdge(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	dbID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerDatabase, "projects/proj-1/instances/inst-1/databases/db-1", "",
		marshalAttrs(t, &spanner.Database{Name: "projects/proj-1/instances/inst-1/databases/db-1"}))

	backupID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerBackup, "projects/proj-1/instances/inst-1/backups/backup-1", "",
		marshalAttrs(t, &spanner.Backup{
			Name:     "projects/proj-1/instances/inst-1/backups/backup-1",
			Database: "projects/proj-1/instances/inst-1/databases/db-1",
		}))

	if err := resolveSpannerBackupRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerBackupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(backupID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != dbID {
		t.Errorf("want only database edge (no encryptionInfo), got %+v", rels)
	}
}

func TestResolveSpannerBackupRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveSpannerBackupRelationships(p, st); err != nil {
		t.Fatalf("resolveSpannerBackupRelationships on empty project: %v", err)
	}
}

// Database -> CryptoKey (encryptionConfig.{kmsKeyName,kmsKeyNames[]}) is
// owned by resolveDatabasesRelationships (databases_resolvers_test.go),
// not this file — see the package doc comment in spanner_resolvers.go.
