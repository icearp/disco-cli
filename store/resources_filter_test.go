package store

import (
	"fmt"
	"testing"
)

// mkAccountNode seeds one current resource in the named account. native is the
// account-local native id, so callers can produce ties across accounts.
func mkAccountNode(t *testing.T, st *Store, account, native string) string {
	t.Helper()
	r := &Resource{
		Provider: "aws", AccountID: account, Type: "aws:ec2:instance",
		NativeID: native, AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsert %s/%s: %v", account, native, err)
	}
	return r.ID
}

// TestListResources_AccountIDs pins the multi-account filter, including the
// case a caller most needs to know about: an empty list is UNFILTERED, not a
// deny. That is the same rule the sibling slice filters follow, and pinning it
// is what keeps a future reader from "fixing" the guard to treat empty as a
// deny — which would silently flip every caller that leaves the field unset.
// An authorization caller must gate on its own scope flag before it gets here.
func TestListResources_AccountIDs(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		mkAccountNode(t, st, "111", "i-a1")
		mkAccountNode(t, st, "222", "i-b1")
		mkAccountNode(t, st, "333", "i-c1")

		accountsOf := func(t *testing.T, f ResourceFilter) []string {
			t.Helper()
			f.Limit = 100
			rows, err := st.ListResources(f)
			if err != nil {
				t.Fatalf("ListResources(%+v): %v", f, err)
			}
			var got []string
			for _, r := range rows {
				got = append(got, r.AccountID)
			}
			return got
		}

		countBy := func(got []string) map[string]int {
			m := map[string]int{}
			for _, a := range got {
				m[a]++
			}
			return m
		}

		t.Run("subset", func(t *testing.T) {
			got := countBy(accountsOf(t, ResourceFilter{AccountIDs: []string{"111", "333"}}))
			if got["111"] != 1 || got["333"] != 1 || got["222"] != 0 {
				t.Errorf("accounts = %v; want one each of 111/333 and none of 222", got)
			}
		})

		t.Run("nil means unfiltered", func(t *testing.T) {
			got := countBy(accountsOf(t, ResourceFilter{AccountIDs: nil}))
			if got["111"] != 1 || got["222"] != 1 || got["333"] != 1 {
				t.Errorf("accounts = %v; want every account", got)
			}
		})

		t.Run("empty means unfiltered, not denied", func(t *testing.T) {
			got := countBy(accountsOf(t, ResourceFilter{AccountIDs: []string{}}))
			if got["111"] != 1 || got["222"] != 1 || got["333"] != 1 {
				t.Errorf("accounts = %v; want every account — an empty list is a filter that is not set, "+
					"and an authorization caller must gate before calling", got)
			}
		})

		t.Run("unknown account matches nothing", func(t *testing.T) {
			if got := accountsOf(t, ResourceFilter{AccountIDs: []string{"999"}}); len(got) != 0 {
				t.Errorf("accounts = %v; want none", got)
			}
		})

		// Documented as composing: both clauses apply, so the result is the
		// intersection. Pinned so a later collapse into one clause, or into an
		// OR, fails here rather than quietly widening a caller's result.
		t.Run("composes with AccountID", func(t *testing.T) {
			if got := accountsOf(t, ResourceFilter{AccountID: "111", AccountIDs: []string{"222"}}); len(got) != 0 {
				t.Errorf("accounts = %v; want none — 111 AND (222) intersect to nothing", got)
			}
			got := countBy(accountsOf(t, ResourceFilter{AccountID: "111", AccountIDs: []string{"111", "222"}}))
			if got["111"] != 1 || got["222"] != 0 {
				t.Errorf("accounts = %v; want only 111", got)
			}
		})
	})
}

// TestListResources_OrderSurvivesARescan pins that the ORDER BY resolves ties
// rather than leaving them to the physical row order. Every seeded row shares
// provider, type and name, so those three order none of them.
//
// The disturbance is a rescan, because that is the one this store performs
// constantly: re-upserting a resource with changed attributes supersedes it and
// writes the new current row somewhere else, without touching a single sort
// key. A partial ORDER BY lets that relocation reorder the result. Measured on
// this fixture with the tiebreaker removed: 59 of 60 rows change position on
// SQLite. Postgres happens to hold its order at this size — which is precisely
// why the assertion runs on both dialects and trusts neither.
//
// Two consumers depend on this. Under a LIMIT, tie order decides WHICH rows a
// truncated read returns at all; and the disco-saas evidence bundle documents
// its resource file as byte-identical across pulls, a claim a rescan between
// two pulls would otherwise break.
func TestListResources_OrderSurvivesARescan(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		const n = 60
		native := func(i int) string { return fmt.Sprintf("i-tie-%04d", i) }
		// Inserted in reverse so physical order never coincides with sorted
		// order: an implementation returning rows in heap order diverges here
		// rather than passing by luck.
		for i := n - 1; i >= 0; i-- {
			mkAccountNode(t, st, "111", native(i))
		}

		ids := func(t *testing.T, limit uint64) []string {
			t.Helper()
			rows, err := st.ListResources(ResourceFilter{Limit: limit})
			if err != nil {
				t.Fatalf("ListResources(limit=%d): %v", limit, err)
			}
			var got []string
			for _, r := range rows {
				got = append(got, r.ID)
			}
			return got
		}

		before := ids(t, n)
		if len(before) != n {
			t.Fatalf("full read = %d rows; want %d", len(before), n)
		}
		// Taken on BOTH sides of the rescan on purpose. Compared only against
		// the full read from the same instant, this assertion passes against
		// the very bug it describes — two back-to-back reads with nothing
		// disturbing them agree even under a partial order. The truncated read
		// has to survive the disturbance to mean anything.
		halfBefore := ids(t, n/2)
		if !equalIDs(halfBefore, before[:n/2]) {
			t.Errorf("LIMIT %d = %v; want the prefix %v", n/2, halfBefore, before[:n/2])
		}

		// Rescan every other resource with changed attributes. Each supersedes,
		// relocating its current row; no sort key changes.
		for i := 0; i < n; i += 2 {
			r := &Resource{
				Provider: "aws", AccountID: "111", Type: "aws:ec2:instance",
				NativeID: native(i), AttributesJSON: `{"v":2}`, DiscoveredBy: testScanID,
			}
			if _, err := st.UpsertResource(r); err != nil {
				t.Fatalf("rescan upsert %s: %v", native(i), err)
			}
		}

		after := ids(t, n)
		if !equalIDs(before, after) {
			moved := 0
			for i := range before {
				if i >= len(after) || before[i] != after[i] {
					moved++
				}
			}
			t.Errorf("a rescan reordered %d of %d rows; the sort keys did not change, so the order must not either",
				moved, len(before))
		}
		// The consequence that actually costs a caller data: under a LIMIT, the
		// tie group straddling the cutoff decides which rows exist at all, so a
		// rescan can change the CONTENTS of a truncated read, not just its order.
		if halfAfter := ids(t, n/2); !equalIDs(halfBefore, halfAfter) {
			t.Errorf("a rescan changed which rows a LIMIT %d read returns:\n before %v\n after  %v",
				n/2, halfBefore, halfAfter)
		}
	})
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
