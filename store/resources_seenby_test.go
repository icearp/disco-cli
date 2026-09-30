package store

import (
	"slices"
	"testing"
)

// A re-verify-only scan inserts nothing, yet `disco scans` counts the rows it
// verified; --scan-id must list those same rows, not zero.
func TestListResources_SeenByMatchesEitherScanColumn(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		row := func(nid, scan string) *Resource {
			return &Resource{Provider: "aws", AccountID: "1", Type: "aws:ec2:volume", NativeID: nid, AttributesJSON: "{}", DiscoveredBy: scan}
		}
		scan := func(id string, rows ...*Resource) {
			t.Helper()
			if _, err := st.CreateScanWithID(id, []string{"aws"}, map[string]any{}); err != nil {
				t.Fatalf("CreateScanWithID(%s): %v", id, err)
			}
			if _, err := st.UpsertResources(rows); err != nil {
				t.Fatalf("upsert %s: %v", id, err)
			}
			if err := st.CompleteScan(id); err != nil {
				t.Fatalf("CompleteScan(%s): %v", id, err)
			}
		}
		scan("seen-a", row("vol-1", "seen-a"), row("vol-2", "seen-a"))
		scan("seen-b", row("vol-1", "seen-b")) // re-verify only
		// A resolver placeholder stamped by seen-b counts toward its
		// resource_count, so the listing must include it too.
		if _, err := st.InsertResourcesIfAbsent([]*Resource{row("vol-ref", "seen-b")}); err != nil {
			t.Fatalf("InsertResourcesIfAbsent: %v", err)
		}
		if err := st.CompleteScan("seen-b"); err != nil {
			t.Fatalf("recount seen-b: %v", err)
		}

		seen := func(id string) []string {
			t.Helper()
			rs, err := st.ListResources(ResourceFilter{SeenBy: id, IncludeManaged: true})
			if err != nil {
				t.Fatalf("ListResources(SeenBy=%s): %v", id, err)
			}
			var out []string
			for _, r := range rs {
				out = append(out, r.NativeID)
			}
			slices.Sort(out)
			return out
		}

		if got, want := seen("seen-b"), []string{"vol-1", "vol-ref"}; !slices.Equal(got, want) {
			t.Errorf("SeenBy(seen-b) = %v, want %v", got, want)
		}
		sc, err := st.GetScan("seen-b")
		if err != nil {
			t.Fatalf("GetScan: %v", err)
		}
		if sc.ResourceCount == nil || *sc.ResourceCount != len(seen("seen-b")) {
			t.Errorf("seen-b resource_count = %v, listing has %d rows", sc.ResourceCount, len(seen("seen-b")))
		}
		// vol-1's verified_by moved to seen-b; it still matches seen-a via discovered_by.
		if got, want := seen("seen-a"), []string{"vol-1", "vol-2"}; !slices.Equal(got, want) {
			t.Errorf("SeenBy(seen-a) = %v, want %v", got, want)
		}
	})
}
