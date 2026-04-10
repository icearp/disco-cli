package gcp

import (
	"path/filepath"
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// testScanID is the fixed scan ID inserted into every test database.
const testScanID = "00000000000000000000000000000000"

// newTestStore opens a temporary SQLite database for use in provider tests
// and inserts a scan record so resources can satisfy the discovered_by FK.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("newTestStore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.DB().Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("newTestStore: insert test scan: %v", err)
	}
	return st
}

// upsertTestResource inserts a minimal resource and returns its computed stable ID.
// Pass an empty region to leave Region unset.
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

// newTestProject returns a minimal project struct for resolver tests.
func newTestProject(id string) *project {
	return &project{ID: id, Name: "Test Project", Number: "123456789"}
}
