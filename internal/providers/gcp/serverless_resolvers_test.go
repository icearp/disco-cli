package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
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
