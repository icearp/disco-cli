package gcp

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

// TestResolveIAMServiceAccountKeyRelationships verifies that an SA key
// resource emits an attached-to edge to its parent SA based on NativeID.
func TestResolveIAMServiceAccountKeyRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	saNative := "projects/my-project/serviceAccounts/sa1@my-project.iam.gserviceaccount.com"
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, saNative, "", "{}")

	keyNative := saNative + "/keys/abcdef0123456789"
	keyID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMSAKey, keyNative, "", "{}")

	// Orphan key (SA not in store) — must be skipped, not error.
	upsertTestResource(t, st, "gcp", p.ID, TypeIAMSAKey,
		"projects/my-project/serviceAccounts/missing@my-project.iam.gserviceaccount.com/keys/xyz",
		"", "{}")

	if err := resolveIAMServiceAccountKeyRelationships(p, st); err != nil {
		t.Fatalf("resolveIAMServiceAccountKeyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(keyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(rels))
	}
	if rels[0].ToID != saID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("unexpected edge: %+v", rels[0])
	}
}
