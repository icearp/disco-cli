package store

import "testing"

// TestInsertResourcesIfAbsent_InsertsWhenAbsent verifies the primitive creates
// a fresh current-version row at the natural key when none exists.
func TestInsertResourcesIfAbsent_InsertsWhenAbsent(t *testing.T) {
	st := openTestStore(t)

	r := &Resource{
		Provider:       "aws",
		AccountID:      "222222222222",
		Type:           "aws:iam:account",
		NativeID:       "arn:aws:iam::222222222222:root",
		Name:           sp("222222222222"),
		AttributesJSON: "{}",
		DiscoveredBy:   testScanID,
	}
	n, err := st.InsertResourcesIfAbsent([]*Resource{r})
	if err != nil {
		t.Fatalf("InsertResourcesIfAbsent: %v", err)
	}
	if n != 1 {
		t.Fatalf("inserted = %d, want 1", n)
	}
	got, err := st.GetResource(r.ID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.AttributesJSON != "{}" {
		t.Errorf("attributes = %q, want empty {}", got.AttributesJSON)
	}
}

// TestInsertResourcesIfAbsent_NoClobber verifies the primitive is a no-op when
// a current-version row already holds the natural key — the existing
// (populated) attributes survive and no version split occurs.
func TestInsertResourcesIfAbsent_NoClobber(t *testing.T) {
	st := openTestStore(t)

	populated := &Resource{
		Provider:       "aws",
		AccountID:      "222222222222",
		Type:           "aws:iam:account",
		NativeID:       "arn:aws:iam::222222222222:root",
		Name:           sp("acme"),
		AttributesJSON: `{"SummaryMap":{"Users":5}}`,
		DiscoveredBy:   testScanID,
	}
	if _, err := st.UpsertResource(populated); err != nil {
		t.Fatalf("seed UpsertResource: %v", err)
	}

	placeholder := &Resource{
		Provider:       "aws",
		AccountID:      "222222222222",
		Type:           "aws:iam:account",
		NativeID:       "arn:aws:iam::222222222222:root",
		Name:           sp("222222222222"),
		AttributesJSON: "{}",
		DiscoveredBy:   testScanID,
	}
	n, err := st.InsertResourcesIfAbsent([]*Resource{placeholder})
	if err != nil {
		t.Fatalf("InsertResourcesIfAbsent: %v", err)
	}
	if n != 0 {
		t.Fatalf("inserted = %d, want 0 (row already present)", n)
	}

	got, err := st.GetResource(populated.ID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.AttributesJSON != `{"SummaryMap":{"Users":5}}` {
		t.Errorf("populated row clobbered: attributes = %q", got.AttributesJSON)
	}
	versions, err := st.GetResourceVersions(populated.ID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Errorf("version count = %d, want 1 (no split)", len(versions))
	}
}

// TestInsertResourcesIfAbsent_PlaceholderThenScanned is the reference-discovery
// round trip: an empty placeholder inserted first, then UpsertResources with
// real attributes version-splits it. The placeholder is preserved in history,
// the current row is populated, and the deterministic root_id (the edge FK
// target) is stable across the split.
func TestInsertResourcesIfAbsent_PlaceholderThenScanned(t *testing.T) {
	st := openTestStore(t)

	key := func() *Resource {
		return &Resource{
			Provider:     "aws",
			AccountID:    "222222222222",
			Type:         "aws:iam:account",
			NativeID:     "arn:aws:iam::222222222222:root",
			DiscoveredBy: testScanID,
		}
	}

	placeholder := key()
	placeholder.Name = sp("222222222222")
	placeholder.AttributesJSON = "{}"
	if _, err := st.InsertResourcesIfAbsent([]*Resource{placeholder}); err != nil {
		t.Fatalf("InsertResourcesIfAbsent: %v", err)
	}
	rootID := placeholder.ID

	scanned := key()
	scanned.Name = sp("acme")
	scanned.AttributesJSON = `{"SummaryMap":{"Users":5}}`
	inserted, err := st.UpsertResources([]*Resource{scanned})
	if err != nil {
		t.Fatalf("UpsertResources: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("version-split inserted = %d, want 1", inserted)
	}
	// root_id is the deterministic hash — identical empty or populated, so the
	// edge target never moves.
	if scanned.ID != rootID {
		t.Errorf("ID changed across discovery: placeholder %q, scanned %q", rootID, scanned.ID)
	}

	got, err := st.GetResource(rootID)
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.AttributesJSON != `{"SummaryMap":{"Users":5}}` {
		t.Errorf("current row not populated: attributes = %q", got.AttributesJSON)
	}
	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("version count = %d, want 2 (empty→populated)", len(versions))
	}
	if versions[0].AttributesJSON != "{}" {
		t.Errorf("history root attributes = %q, want empty {}", versions[0].AttributesJSON)
	}
}
