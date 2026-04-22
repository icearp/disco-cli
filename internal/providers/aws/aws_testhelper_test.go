package aws

import (
	"path/filepath"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const (
	// testScanID is the fixed scan ID inserted into every test database.
	testScanID = "00000000000000000000000000000000"
	// testAccountID and testRegion are canonical values shared across resolver tests.
	testAccountID = "123456789012"
	testRegion    = "us-east-1"
)

// newTestStore opens a temporary SQLite database for use in provider tests
// and inserts a scan record so resources can satisfy the discovered_by FK.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("newTestStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.DB().Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("newTestStore: insert test scan: %v", err)
	}
	return st
}

// upsertTestResource inserts a minimal resource with the given AttributesJSON
// and returns its computed stable ID. Pass an empty region to leave Region unset.
func upsertTestResource(t *testing.T, st *store.Store, provider, accountID, rtype, nativeID, region, attrsJSON string) string {
	t.Helper()
	r := &store.Resource{
		Provider:       provider,
		AccountID:      accountID,
		Type:           rtype,
		NativeID:       nativeID,
		AttributesJSON: attrsJSON,
		DiscoveredBy:   testScanID,
	}
	if region != "" {
		r.Region = &region
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsertTestResource %s/%s: %v", rtype, nativeID, err)
	}
	return store.ResourceID(provider, accountID, rtype, nativeID)
}

// newTestAccount returns a minimal account struct for use in resolver tests.
func newTestAccount(id string) *account {
	return &account{ID: id, Name: "Test Account", Regions: []string{"us-east-1"}}
}

// assertRelationship fails the test if no relationship with the given
// (from, to, kind) exists in the rels slice.
func assertRelationship(t *testing.T, rels []store.Relationship, fromID, toID, kind string) {
	t.Helper()
	for _, r := range rels {
		if r.FromID == fromID && r.ToID == toID && r.Kind == kind {
			return
		}
	}
	t.Errorf("missing relationship: %s -[%s]-> %s", fromID, kind, toID)
}
