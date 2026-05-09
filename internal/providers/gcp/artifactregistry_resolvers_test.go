package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveArtifactRegistryRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us/keyRings/r/cryptoKeys/k"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us", "{}")

	repoID := upsertTestResource(t, st, "gcp", p.ID, TypeArtifactRepository,
		"projects/my-project/locations/us/repositories/docker", "us",
		`{"kmsKeyName": "`+keyName+`"}`)
	upsertTestResource(t, st, "gcp", p.ID, TypeArtifactRepository,
		"projects/my-project/locations/us/repositories/plain", "us", `{}`)

	if err := resolveArtifactRegistryRelationships(p, st); err != nil {
		t.Fatalf("resolveArtifactRegistryRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(repoID)
	if len(rels) != 1 || rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
		t.Errorf("got %+v, want →key uses", rels)
	}
}
