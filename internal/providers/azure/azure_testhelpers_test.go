package azure

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/fake"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
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
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.DB().Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, datetime('now'), 'running', '["test"]', '{}')`, testScanID); err != nil {
		t.Fatalf("newTestStore: insert test scan: %v", err)
	}
	// Direction invariant — see aws_testhelper_test.go for rationale.
	t.Cleanup(func() {
		rows, err := st.ReversedContainsEdges()
		if err != nil {
			t.Errorf("ReversedContainsEdges: %v", err)
			return
		}
		if len(rows) > 0 {
			t.Errorf("reversed contains edges leaked: %d rows; first %+v", len(rows), rows[0])
		}
	})
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
	return store.ResourceID(provider, accountID, nativeID)
}

// newTestSubscription returns a minimal subscription struct for resolver tests.
func newTestSubscription(id string) *subscription {
	return &subscription{ID: id, Name: "Test Subscription"}
}

// marshalAttrs JSON-encodes v as the attrsJSON value scanners persist to the
// store. Tests use this with real SDK structs (e.g. armcompute.Disk,
// armnetwork.VirtualNetwork) so SDK schema drift is a Go compile error, not a
// silent resolver edge-loss from stale hand-rolled JSON literals.
func marshalAttrs(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalAttrs: %v", err)
	}
	return string(b)
}

// fakeCred returns an azcore.TokenCredential the SDK auth pipeline accepts
// without a real auth call. Paired with a fake transport, no HTTP or token
// exchange occurs.
func fakeCred() azcore.TokenCredential { return &fake.TokenCredential{} }

// fakeClientOptions returns *arm.ClientOptions wired to short-circuit every
// request through the supplied fake transport. Tests use this in place of
// azClientOptions when constructing arm* clients so NewListPager and friends
// never touch the network.
//
// Retry is collapsed (MaxRetries=0): a fake transport returning a
// deterministic response should never need a retry; if it does, the test
// wants the error surfaced immediately, not masked by the production retry
// loop.
func fakeClientOptions(t *testing.T, transport policy.Transporter) *arm.ClientOptions {
	t.Helper()
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: transport,
			Retry:     policy.RetryOptions{MaxRetries: 0},
		},
	}
}
