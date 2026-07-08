package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveBQConnectionRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey,
		"projects/proj1/locations/us/keyRings/kr1/cryptoKeys/key1", "us", "{}")
	sqlID := upsertTestResource(t, st, "gcp", p.ID, TypeSQLInstance,
		"projects/proj1/instances/inst1", "us-central1", "{}")
	spannerID := upsertTestResource(t, st, "gcp", p.ID, TypeSpannerDatabase,
		"projects/proj1/instances/spinst1/databases/db1", "", "{}")

	connID := upsertTestResource(t, st, "gcp", p.ID, TypeBQConnection,
		"projects/proj1/locations/us/connections/conn1", "us", `{
			"kmsKeyName": "projects/proj1/locations/us/keyRings/kr1/cryptoKeys/key1/cryptoKeyVersions/3",
			"cloudSql": {"instanceId": "proj1:us-central1:inst1"},
			"cloudSpanner": {"database": "projects/proj1/instances/spinst1/databases/db1"}
		}`)

	if err := resolveBQConnectionRelationships(p, st); err != nil {
		t.Fatalf("resolveBQConnectionRelationships: %v", err)
	}

	got, err := st.RelationshipsFrom(connID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(RelationshipsFrom) = %d, want 3: %+v", len(got), got)
	}
	want := map[string]bool{keyID: false, sqlID: false, spannerID: false}
	for _, rel := range got {
		if rel.Kind != store.RelUses {
			t.Errorf("rel.Kind = %q, want %q", rel.Kind, store.RelUses)
		}
		if _, ok := want[rel.ToID]; !ok {
			t.Errorf("unexpected edge target %q", rel.ToID)
			continue
		}
		want[rel.ToID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("missing edge to %q", id)
		}
	}
}

func TestResolveBQConnectionRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	connID := upsertTestResource(t, st, "gcp", p.ID, TypeBQConnection,
		"projects/proj1/locations/us/connections/conn1", "us", `{
			"kmsKeyName": "projects/proj1/locations/us/keyRings/kr1/cryptoKeys/key1",
			"cloudSql": {"instanceId": "proj1:us-central1:inst1"},
			"cloudSpanner": {"database": "projects/proj1/instances/spinst1/databases/db1"}
		}`)

	if err := resolveBQConnectionRelationships(p, st); err != nil {
		t.Fatalf("resolveBQConnectionRelationships: %v", err)
	}

	got, err := st.RelationshipsFrom(connID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(RelationshipsFrom) = %d, want 0 (no scanned targets): %+v", len(got), got)
	}
}

func TestResolveBQConnectionRelationships_NoAttrsNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	connID := upsertTestResource(t, st, "gcp", p.ID, TypeBQConnection,
		"projects/proj1/locations/us/connections/conn1", "us", `{}`)

	if err := resolveBQConnectionRelationships(p, st); err != nil {
		t.Fatalf("resolveBQConnectionRelationships: %v", err)
	}

	got, err := st.RelationshipsFrom(connID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len(RelationshipsFrom) = %d, want 0: %+v", len(got), got)
	}
}

func TestResolveBQConnectionRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	if err := resolveBQConnectionRelationships(p, st); err != nil {
		t.Fatalf("resolveBQConnectionRelationships on empty project: %v", err)
	}
}

func TestSQLInstanceNativeFromColonID(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"valid", "proj1:us-central1:inst1", "projects/proj1/instances/inst1", true},
		{"missing segments", "proj1:inst1", "", false},
		{"empty project", ":us-central1:inst1", "", false},
		{"empty instance", "proj1:us-central1:", "", false},
		{"empty string", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := sqlInstanceNativeFromColonID(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("sqlInstanceNativeFromColonID(%q) = (%q, %v); want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
