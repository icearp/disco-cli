package gcp

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveSecretRelationships_AutomaticCMEK verifies that a secret with
// replication.automatic.customerManagedEncryption produces a uses edge.
func TestResolveSecretRelationships_AutomaticCMEK(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us-central1/keyRings/r1/cryptoKeys/k1"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us-central1", "{}")

	attrs := `{
		"replication": {
			"automatic": {
				"customerManagedEncryption": {"kmsKeyName": "` + keyName + `"}
			}
		}
	}`
	secretID := upsertTestResource(t, st, "gcp", p.ID, TypeSecret,
		"projects/my-project/secrets/my-secret", "", attrs)

	if err := resolveSecretRelationships(p, st); err != nil {
		t.Fatalf("resolveSecretRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(secretID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != keyID || rels[0].Kind != store.RelUses {
		t.Fatalf("expected 1 uses edge to %s, got %+v", keyID, rels)
	}
}

// TestResolveSecretRelationships_UserManagedDedup verifies user-managed
// replicas with the same CMEK across two locations only emit one edge.
func TestResolveSecretRelationships_UserManagedDedup(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us-central1/keyRings/r1/cryptoKeys/k1"
	upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us-central1", "{}")

	attrs := `{
		"replication": {
			"userManaged": {
				"replicas": [
					{"location": "us-central1", "customerManagedEncryption": {"kmsKeyName": "` + keyName + `"}},
					{"location": "us-east1",    "customerManagedEncryption": {"kmsKeyName": "` + keyName + `"}}
				]
			}
		}
	}`
	secretID := upsertTestResource(t, st, "gcp", p.ID, TypeSecret,
		"projects/my-project/secrets/my-secret", "", attrs)

	if err := resolveSecretRelationships(p, st); err != nil {
		t.Fatalf("resolveSecretRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(secretID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 deduped edge, got %d: %+v", len(rels), rels)
	}
}

// TestResolveSecretRelationships_NoCMEK verifies a Google-managed-encryption
// secret produces no edges and no errors.
func TestResolveSecretRelationships_NoCMEK(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	keyName := "projects/my-project/locations/us-central1/keyRings/r1/cryptoKeys/k1"
	upsertTestResource(t, st, "gcp", p.ID, TypeKMSCryptoKey, keyName, "us-central1", "{}")

	secretID := upsertTestResource(t, st, "gcp", p.ID, TypeSecret,
		"projects/my-project/secrets/plain", "", `{"replication": {"automatic": {}}}`)

	if err := resolveSecretRelationships(p, st); err != nil {
		t.Fatalf("resolveSecretRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(secretID)
	if len(rels) != 0 {
		t.Errorf("expected 0 edges, got %d", len(rels))
	}
}
