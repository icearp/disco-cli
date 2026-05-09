package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveComposerRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us-central1/keyRings/r/cryptoKeys/k"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us-central1", "{}")

	saEmail := "composer-sa@my-project.iam.gserviceaccount.com"
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount,
		"projects/my-project/serviceAccounts/"+saEmail, "", "{}")

	envID := upsertTestResource(t, st, "gcp", p.ID, TypeComposerEnv,
		"projects/my-project/locations/us-central1/environments/airflow", "us-central1",
		`{"config": {"encryptionConfig": {"kmsKeyName": "`+keyName+`"}, "nodeConfig": {"serviceAccount": "`+saEmail+`"}}}`)

	if err := resolveComposerRelationships(p, st); err != nil {
		t.Fatalf("resolveComposerRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(envID)
	got := map[string]bool{}
	for _, r := range rels {
		got[r.ToID] = true
	}
	if !got[keyID] || !got[saID] {
		t.Errorf("env edges: got %+v, want →key + →SA", rels)
	}
	for _, r := range rels {
		if r.Kind != store.RelUses {
			t.Errorf("expected uses kind, got %s", r.Kind)
		}
	}
}
