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

func TestResolveBigQueryRowAccessPolicyRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	saEmail := "rap-sa@my-project.iam.gserviceaccount.com"
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount,
		"projects/my-project/serviceAccounts/"+saEmail, "", "{}")

	userEmail := "alice@example.com"
	userID := upsertTestResource(t, st, "gcp", "C0123abc", TypeWorkspaceUser, "users/u1", "",
		`{"primaryEmail": "`+userEmail+`"}`)

	groupEmail := "eng-team@example.com"
	groupID := upsertTestResource(t, st, "gcp", "C0123abc", TypeCloudIdentityGroup, "groups/g1", "",
		`{"groupKey": {"id": "`+groupEmail+`"}}`)

	rapAttrs := `{"iamPolicy": {"bindings": [
		{"role": "roles/bigquery.dataViewer", "members": [
			"serviceAccount:` + saEmail + `",
			"user:` + userEmail + `",
			"group:` + groupEmail + `",
			"domain:example.com",
			"allUsers"
		]}
	]}}`
	rapID := upsertTestResource(t, st, "gcp", p.ID, TypeBQRowAccessPolicy,
		"projects/my-project/datasets/ds1/tables/t1/rowAccessPolicies/rap1", "", rapAttrs)

	if err := resolveBigQueryRowAccessPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRowAccessPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(rapID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if len(got) != 3 || got[saID] != store.RelUses || got[userID] != store.RelUses || got[groupID] != store.RelUses {
		t.Errorf("got %+v, want exactly →SA + →workspaceUser + →group (uses), no domain/allUsers edges", got)
	}
}

func TestResolveBigQueryRowAccessPolicyRelationships_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	rapAttrs := `{"iamPolicy": {"bindings": [
		{"role": "roles/bigquery.dataViewer", "members": [
			"serviceAccount:not-scanned@my-project.iam.gserviceaccount.com",
			"user:not-scanned@example.com",
			"group:not-scanned@example.com"
		]}
	]}}`
	rapID := upsertTestResource(t, st, "gcp", p.ID, TypeBQRowAccessPolicy,
		"projects/my-project/datasets/ds1/tables/t1/rowAccessPolicies/rap1", "", rapAttrs)

	if err := resolveBigQueryRowAccessPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRowAccessPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(rapID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges for unscanned grantees, got %+v", rels)
	}
}

// TestResolveBigQueryRowAccessPolicyRelationships_NoIamPolicyNoPanic covers
// the pre-R30 shape (no iamPolicy fetched, e.g. the GetIamPolicy call was
// denied) — must not panic and must produce no edges.
func TestResolveBigQueryRowAccessPolicyRelationships_NoIamPolicyNoPanic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	rapID := upsertTestResource(t, st, "gcp", p.ID, TypeBQRowAccessPolicy,
		"projects/my-project/datasets/ds1/tables/t1/rowAccessPolicies/rap1", "", `{}`)

	if err := resolveBigQueryRowAccessPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRowAccessPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(rapID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edges when iamPolicy is unset, got %+v", rels)
	}
}

func TestResolveBigQueryRowAccessPolicyRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveBigQueryRowAccessPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveBigQueryRowAccessPolicyRelationships on empty project: %v", err)
	}
}
