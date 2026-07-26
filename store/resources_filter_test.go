package store

import "testing"

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
