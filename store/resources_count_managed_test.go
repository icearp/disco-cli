package store

import "testing"

// TestCountManaged_CountsCurrentManagedRowsOnly pins both halves of the
// contract CountManaged had wrong, and it runs under withDialects because
// exactly one of them is invisible on SQLite.
//
//   - The comparison. `managed_by_provider = 1` against a real BOOLEAN column
//     is 42883 on Postgres, while SQLite stores booleans as 0/1 and accepts it.
//     A SQLite-only test certifies a query that cannot run on the backend the
//     hosted product uses, which is how the defect survived.
//
//   - The population. `resources` keeps every version forever, so a managed
//     resource that has been re-scanned with changed attributes leaves a
//     superseded row behind. Counting both rows makes the number grow with
//     history rather than with the estate — and `disco check` prints it beside
//     a listing that IS scoped to current rows, so the two would be counted
//     over different populations.
//
// The fixture therefore holds three rows: one unmanaged resource, and one
// managed resource upserted twice so its chain has a superseded row and a
// current one. Every wrong version of the query returns 2 or errors; only the
// correct one returns 1.
func TestCountManaged_CountsCurrentManagedRowsOnly(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		unmanaged := &Resource{
			Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance",
			NativeID: "i-user", AttributesJSON: `{"v":1}`, DiscoveredBy: testScanID,
		}
		if _, err := st.UpsertResource(unmanaged); err != nil {
			t.Fatalf("upsert unmanaged: %v", err)
		}

		managed := func(attrs string) *Resource {
			return &Resource{
				Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance",
				NativeID: "i-mgd", AttributesJSON: attrs, DiscoveredBy: testScanID,
				ManagedByProvider: true,
			}
		}
		first := managed(`{"v":1}`)
		if _, err := st.UpsertResource(first); err != nil {
			t.Fatalf("upsert managed v1: %v", err)
		}
		if _, err := st.UpsertResource(managed(`{"v":2}`)); err != nil {
			t.Fatalf("upsert managed v2: %v", err)
		}

		// Anchor the fixture: without a real supersede the current-row half of
		// the assertion is vacuous, and it would read as passing.
		versions, err := st.GetResourceVersions(first.ID)
		if err != nil {
			t.Fatalf("GetResourceVersions: %v", err)
		}
		if len(versions) != 2 {
			t.Fatalf("fixture: want a 2-row version chain, got %d", len(versions))
		}

		got, err := st.CountManaged()
		if err != nil {
			t.Fatalf("CountManaged: %v", err)
		}
		if got != 1 {
			t.Errorf("CountManaged: got %d, want 1 (one current managed row; the superseded version and the unmanaged resource must not count)", got)
		}
	})
}
