package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveBigQueryRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us/keyRings/r/cryptoKeys/k"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us", "{}")

	dsCMEK := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset, "my-project:secure", "us",
		`{"defaultEncryptionConfiguration": {"kmsKeyName": "`+keyName+`"}}`)
	dsPlain := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset, "my-project:plain", "us", `{}`)

	if err := resolveBigQueryRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dsCMEK)
	if len(rels) != 1 || rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
		t.Errorf("CMEK dataset: got %+v, want →key uses", rels)
	}
	relsPlain, _ := st.RelationshipsFrom(dsPlain)
	if len(relsPlain) != 0 {
		t.Errorf("plain dataset: expected no edges, got %+v", relsPlain)
	}
}

// TestResolveBigQueryRelationships_AuthorizedAccess covers all three
// `access[]` grant kinds (view/routine/dataset), all same-project, plus a
// cross-project view grant that must be silently skipped (scope decision:
// same-project only this wave).
func TestResolveBigQueryRelationships_AuthorizedAccess(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	tableID := upsertTestResource(t, st, "gcp", p.ID, TypeBQTable, "opaque-table-id", "us",
		`{"tableReference":{"projectId":"my-project","datasetId":"other","tableId":"t1"}}`)
	routineID := upsertTestResource(t, st, "gcp", p.ID, TypeBQRoutine,
		"projects/my-project/datasets/other/routines/r1", "",
		`{"routineReference":{"projectId":"my-project","datasetId":"other","routineId":"r1"}}`)
	sharedID := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset, "my-project:shared", "us",
		`{"datasetReference":{"projectId":"my-project","datasetId":"shared"}}`)

	sourceAttrs := `{
		"access": [
			{"view": {"projectId": "my-project", "datasetId": "other", "tableId": "t1"}},
			{"routine": {"projectId": "my-project", "datasetId": "other", "routineId": "r1"}},
			{"dataset": {"dataset": {"projectId": "my-project", "datasetId": "shared"}, "targetTypes": ["VIEWS"]}},
			{"view": {"projectId": "cross-project", "datasetId": "x", "tableId": "y"}}
		]
	}`
	sourceID := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset, "my-project:source", "us", sourceAttrs)

	if err := resolveBigQueryRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(sourceID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("expected 3 edges (view + routine + dataset), got %d: %+v", len(rels), rels)
	}
	hits := map[string]bool{}
	for _, r := range rels {
		if r.Kind != store.RelUses {
			t.Errorf("expected RelUses, got %q for %+v", r.Kind, r)
		}
		hits[r.ToID] = true
	}
	if !hits[tableID] {
		t.Errorf("missing edge to authorized view (table) %q; got %+v", tableID, rels)
	}
	if !hits[routineID] {
		t.Errorf("missing edge to authorized routine %q; got %+v", routineID, rels)
	}
	if !hits[sharedID] {
		t.Errorf("missing edge to authorized dataset %q; got %+v", sharedID, rels)
	}
}

// TestResolveBigQueryRelationships_AuthorizedAccessUnscannedSkipped verifies
// grants referencing tables/routines/datasets not in the store produce no
// edges and no panic.
func TestResolveBigQueryRelationships_AuthorizedAccessUnscannedSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	sourceAttrs := `{
		"access": [
			{"view": {"projectId": "my-project", "datasetId": "other", "tableId": "not-scanned"}},
			{"routine": {"projectId": "my-project", "datasetId": "other", "routineId": "not-scanned"}},
			{"dataset": {"dataset": {"projectId": "my-project", "datasetId": "not-scanned"}, "targetTypes": ["VIEWS"]}}
		]
	}`
	sourceID := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset, "my-project:source", "us", sourceAttrs)

	if err := resolveBigQueryRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(sourceID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned targets, got %+v", rels)
	}
}

// TestResolveBigQueryRelationships_NoAccessArrayNoPanic guards the nil-attrs
// / missing-access-array case.
func TestResolveBigQueryRelationships_NoAccessArrayNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	sourceID := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset, "my-project:source", "us", `{}`)

	if err := resolveBigQueryRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(sourceID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges, got %+v", rels)
	}
}

// TestResolveBigQueryRelationships_AuthorizedDatasetSelfLoopSkipped guards
// against a dataset whose own `access[].dataset` entry names itself — no
// self-loop edge should be created.
func TestResolveBigQueryRelationships_AuthorizedDatasetSelfLoopSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	sourceAttrs := `{
		"datasetReference": {"projectId": "my-project", "datasetId": "source"},
		"access": [
			{"dataset": {"dataset": {"projectId": "my-project", "datasetId": "source"}, "targetTypes": ["VIEWS"]}}
		]
	}`
	sourceID := upsertTestResource(t, st, "gcp", p.ID, TypeBQDataset, "my-project:source", "us", sourceAttrs)

	if err := resolveBigQueryRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(sourceID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no self-loop edge, got %+v", rels)
	}
}

// TestResolveBigQueryRelationships_EmptyProjectNoResources guards the
// whole-function no-datasets-at-all case.
func TestResolveBigQueryRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveBigQueryRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRelationships on empty project: %v", err)
	}
}
