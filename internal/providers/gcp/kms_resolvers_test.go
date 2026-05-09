package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveKMSRelationships verifies that a bucket's defaultKmsKeyName
// produces a `uses` edge to the matching cryptoKey resource. Also covers
// the cryptoKeyVersion-suffix stripping path.
func TestResolveKMSRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us-central1/keyRings/r1/cryptoKeys/k1"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us-central1", "{}")

	// Bucket pinned to a specific cryptoKey version — resolver must strip
	// the trailing /cryptoKeyVersions/N before matching.
	bucketAttrs := `{"encryption": {"defaultKmsKeyName": "` + keyName + `/cryptoKeyVersions/3"}}`
	bucketID := upsertTestResource(t, st, "gcp", p.ID, TypeStorageBucket,
		"https://www.googleapis.com/storage/v1/b/my-bucket", "us-central1", bucketAttrs)

	// Bucket without CMEK — no edge expected.
	upsertTestResource(t, st, "gcp", p.ID, TypeStorageBucket,
		"https://www.googleapis.com/storage/v1/b/plain", "us-central1", `{}`)

	if err := resolveKMSRelationships(p, st); err != nil {
		t.Fatalf("resolveKMSRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(bucketID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(rels))
	}
	if rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
		t.Errorf("unexpected edge: %+v", rels[0])
	}
}

func TestStripCryptoKeyVersion(t *testing.T) {
	tests := map[string]string{
		"projects/p/locations/l/keyRings/r/cryptoKeys/k":                       "projects/p/locations/l/keyRings/r/cryptoKeys/k",
		"projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1":   "projects/p/locations/l/keyRings/r/cryptoKeys/k",
		"projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/123": "projects/p/locations/l/keyRings/r/cryptoKeys/k",
	}
	for in, want := range tests {
		if got := stripCryptoKeyVersion(in); got != want {
			t.Errorf("stripCryptoKeyVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
