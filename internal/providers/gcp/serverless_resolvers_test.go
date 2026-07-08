package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveServerlessRelationships covers Cloud Function → SA + cryptoKey
// and Cloud Run service → SA. Cross-project SA references must be skipped.
func TestResolveServerlessRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	saEmail := "fn-sa@my-project.iam.gserviceaccount.com"
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount,
		"projects/my-project/serviceAccounts/"+saEmail, "", "{}")

	keyName := "projects/my-project/locations/us-central1/keyRings/r/cryptoKeys/k"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us-central1", "{}")

	fnID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudFunction,
		"projects/my-project/locations/us-central1/functions/fn-1", "us-central1",
		`{"kmsKeyName": "`+keyName+`", "serviceConfig": {"serviceAccountEmail": "`+saEmail+`"}}`)

	runID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunSvc,
		"projects/my-project/locations/us-central1/services/svc-1", "us-central1",
		`{"template": {"serviceAccount": "`+saEmail+`"}}`)

	// Cross-project SA — must be skipped without error.
	upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunSvc,
		"projects/my-project/locations/us-central1/services/svc-2", "us-central1",
		`{"template": {"serviceAccount": "other@other-project.iam.gserviceaccount.com"}}`)

	if err := resolveServerlessRelationships(p, st); err != nil {
		t.Fatalf("resolveServerlessRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(fnID)
	got := map[string]string{}
	for _, r := range rels {
		got[r.ToID] = r.Kind
	}
	if got[saID] != store.RelUses || got[keyID] != store.RelUses {
		t.Errorf("function edges: got %+v, want →SA + →key", got)
	}

	relsRun, _ := st.RelationshipsFrom(runID)
	if len(relsRun) != 1 || relsRun[0].ToID != saID {
		t.Errorf("run edge: got %+v, want →SA", relsRun)
	}
}

// TestResolveCloudRunChildRelationships covers Revision/Instance (flat
// serviceAccount/encryptionKey fields), WorkerPool (nested under template),
// and DomainMapping → Service (bare Knative route name, not Service's full
// run/v2 resource name).
func TestResolveCloudRunChildRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	saEmail := "run-sa@my-project.iam.gserviceaccount.com"
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount,
		"projects/my-project/serviceAccounts/"+saEmail, "", "{}")

	keyName := "projects/my-project/locations/us-central1/keyRings/r/cryptoKeys/k"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us-central1", "{}")

	revID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunRevision,
		"projects/my-project/locations/us-central1/services/svc-1/revisions/svc-1-00001", "us-central1",
		`{"serviceAccount": "`+saEmail+`", "encryptionKey": "`+keyName+`/cryptoKeyVersions/1"}`)

	instID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunInstance,
		"projects/my-project/locations/us-central1/instances/inst-1", "us-central1",
		`{"serviceAccount": "`+saEmail+`", "encryptionKey": "`+keyName+`"}`)

	wpID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunWorkerPool,
		"projects/my-project/locations/us-central1/workerPools/wp-1", "us-central1",
		`{"template": {"serviceAccount": "`+saEmail+`", "encryptionKey": "`+keyName+`"}}`)

	svcID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunSvc,
		"projects/my-project/locations/us-central1/services/svc-1", "us-central1", "{}")

	dmID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunDomainMapping,
		"projects/my-project/domainMappings/example.com", "",
		`{"spec": {"routeName": "svc-1"}}`)

	if err := resolveCloudRunChildRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudRunChildRelationships: %v", err)
	}

	for label, id := range map[string]string{"revision": revID, "instance": instID} {
		rels, _ := st.RelationshipsFrom(id)
		got := map[string]string{}
		for _, r := range rels {
			got[r.ToID] = r.Kind
		}
		if got[saID] != store.RelUses || got[keyID] != store.RelUses {
			t.Errorf("%s edges: got %+v, want →SA + →key", label, got)
		}
	}

	wpRels, _ := st.RelationshipsFrom(wpID)
	gotWP := map[string]string{}
	for _, r := range wpRels {
		gotWP[r.ToID] = r.Kind
	}
	if gotWP[saID] != store.RelUses || gotWP[keyID] != store.RelUses {
		t.Errorf("workerPool edges: got %+v, want →SA + →key", gotWP)
	}

	dmRels, _ := st.RelationshipsFrom(dmID)
	if len(dmRels) != 1 || dmRels[0].ToID != svcID || dmRels[0].Kind != store.RelRoutesTo {
		t.Errorf("domainMapping edge: got %+v, want →service routes-to", dmRels)
	}
}

// TestResolveCloudRunChildRelationships_UnmatchedRefsSkipped covers the
// negative space: unscanned SA/key/service targets, and a domain mapping
// with no routeName.
func TestResolveCloudRunChildRelationships_UnmatchedRefsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	revID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunRevision,
		"projects/my-project/locations/us-central1/services/svc-1/revisions/svc-1-00001", "us-central1",
		`{"serviceAccount": "not-scanned@my-project.iam.gserviceaccount.com", "encryptionKey": "projects/my-project/locations/us-central1/keyRings/r/cryptoKeys/not-scanned"}`)

	dmID := upsertTestResource(t, st, "gcp", p.ID, TypeCloudRunDomainMapping,
		"projects/my-project/domainMappings/example.com", "", `{"spec": {}}`)

	if err := resolveCloudRunChildRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudRunChildRelationships: %v", err)
	}

	revRels, _ := st.RelationshipsFrom(revID)
	if len(revRels) != 0 {
		t.Errorf("revision: want no edges for unscanned SA/key, got %+v", revRels)
	}
	dmRels, _ := st.RelationshipsFrom(dmID)
	if len(dmRels) != 0 {
		t.Errorf("domainMapping: want no edge for empty routeName, got %+v", dmRels)
	}
}

func TestResolveCloudRunChildRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	if err := resolveCloudRunChildRelationships(p, st); err != nil {
		t.Fatalf("resolveCloudRunChildRelationships on empty project: %v", err)
	}
}
