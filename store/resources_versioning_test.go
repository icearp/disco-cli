package store

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

// mustQuote returns s as a JSON string literal (quoted + escaped), so a JSON
// document can carry it as an opaque embedded-JSON string value — mirroring how
// AWS returns IAM/KMS policy documents inside AttributesJSON.
func mustQuote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("mustQuote: %v", err)
	}
	return string(b)
}

// ensureTestScan inserts a stub scan row so DiscoveredBy/VerifiedBy
// FK constraints are satisfied. Idempotent — OR IGNORE swallows the
// duplicate on repeat calls.
func ensureTestScan(t *testing.T, st *Store, id string) {
	t.Helper()
	if _, err := st.db.Exec(
		`INSERT OR IGNORE INTO scans (id, started_at, status, providers, scope)
		 VALUES (?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), 'running', '["test"]', '{}')`, id); err != nil {
		t.Fatalf("ensureTestScan: %v", err)
	}
}

// upsertOne drives UpsertResource with the natural-key fields filled
// in. Returns the deterministic ResourceID hash for downstream lookups
// (equals r.ID under both builds).
func upsertOne(t *testing.T, st *Store, attrs, tags string, scanID string) string {
	t.Helper()
	ensureTestScan(t, st, scanID)
	r := &Resource{
		Provider:       "aws",
		AccountID:      "acct",
		Type:           "aws:ec2:instance",
		NativeID:       "i-vers-1",
		AttributesJSON: attrs,
		TagsJSON:       sp(tags),
		DiscoveredBy:   scanID,
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	return r.ID
}

func TestVersioning_FirstDiscovery_SingleRootRow(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{"env":"prod"}`, testScanID)

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("want 1 row in chain, got %d", len(versions))
	}
	v := versions[0]
	if v.RootID != rootID {
		t.Errorf("RootID: got %q want %q", v.RootID, rootID)
	}
	if v.PreviousVersionID != nil {
		t.Errorf("PreviousVersionID: want nil, got %v", *v.PreviousVersionID)
	}
	if v.SupersededBy != nil {
		t.Errorf("SupersededBy: want nil (current row), got %v", *v.SupersededBy)
	}
	if v.VerifiedAt == nil || *v.VerifiedAt == "" {
		t.Error("VerifiedAt should be set on first discovery")
	}
}

func TestVersioning_UnchangedRescan_VerifyOnly(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{"env":"prod"}`, "scan-A")

	// Re-upsert with identical attributes + tags but a different scan id.
	ensureTestScan(t, st, "scan-B")
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-vers-1",
		AttributesJSON: `{"a":1}`,
		TagsJSON:       sp(`{"env":"prod"}`),
		DiscoveredBy:   "scan-B",
	}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("unchanged rescan must NOT split: got %d rows", len(versions))
	}
	v := versions[0]
	if v.VerifiedBy == nil || *v.VerifiedBy != "scan-B" {
		t.Errorf("VerifiedBy should advance to scan-B, got %v", v.VerifiedBy)
	}
	if v.DiscoveredBy != "scan-A" {
		t.Errorf("DiscoveredBy should remain scan-A, got %q", v.DiscoveredBy)
	}
}

func TestVersioning_ChangedAttributes_VersionSplit(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{"env":"prod"}`, "scan-A")

	ensureTestScan(t, st, "scan-B")
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-vers-1",
		AttributesJSON: `{"a":2}`,
		TagsJSON:       sp(`{"env":"prod"}`),
		DiscoveredBy:   "scan-B",
	}); err != nil {
		t.Fatalf("split upsert: %v", err)
	}

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("attribute change must split: got %d rows", len(versions))
	}
	old, cur := versions[0], versions[1]
	if old.AttributesJSON != `{"a":1}` {
		t.Errorf("old row attributes frozen: got %q", old.AttributesJSON)
	}
	if cur.AttributesJSON != `{"a":2}` {
		t.Errorf("current row attributes: got %q", cur.AttributesJSON)
	}
	if old.SupersededBy == nil || *old.SupersededBy != cur.VersionRowID {
		t.Errorf("old.SupersededBy must point at current row, got %v", old.SupersededBy)
	}
	if cur.PreviousVersionID == nil || *cur.PreviousVersionID != old.VersionRowID {
		t.Errorf("current.PreviousVersionID must point at old row, got %v", cur.PreviousVersionID)
	}
	if cur.RootID != old.RootID {
		t.Errorf("RootID must be shared, got old=%q new=%q", old.RootID, cur.RootID)
	}
	if cur.DiscoveredAt != old.DiscoveredAt {
		t.Errorf("DiscoveredAt must inherit from root, got old=%q new=%q",
			old.DiscoveredAt, cur.DiscoveredAt)
	}
}

func TestVersioning_ChangedTags_VersionSplit(t *testing.T) {
	st := openTestStore(t)
	rootID := upsertOne(t, st, `{"a":1}`, `{"env":"prod"}`, "scan-A")

	ensureTestScan(t, st, "scan-B")
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-vers-1",
		AttributesJSON: `{"a":1}`,
		TagsJSON:       sp(`{"env":"staging"}`),
		DiscoveredBy:   "scan-B",
	}); err != nil {
		t.Fatalf("tag-change upsert: %v", err)
	}

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("tag change must split: got %d rows", len(versions))
	}
}

// TestVersioning_ReorderedEmbeddedPolicy_NoSplit guards Fix 1: AWS returns
// embedded policy-document JSON strings (KMS key Policy, S3/SNS/SQS resource
// policies, IAM assume-role docs) with Condition-map keys in non-deterministic
// order. The same policy with reordered keys must NOT version-split.
func TestVersioning_ReorderedEmbeddedPolicy_NoSplit(t *testing.T) {
	st := openTestStore(t)

	// Policy is an opaque JSON *string* whose inner Condition.StringEquals
	// map has two keys; the second upsert swaps their order.
	const polA = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Condition":{"StringEquals":{"kms:CallerAccount":"111","kms:ViaService":"lambda.amazonaws.com"}}}]}`
	const polB = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Condition":{"StringEquals":{"kms:ViaService":"lambda.amazonaws.com","kms:CallerAccount":"111"}}}]}`
	attrsA := `{"Metadata":{"KeyId":"k-1"},"Policy":` + mustQuote(t, polA) + `}`
	attrsB := `{"Metadata":{"KeyId":"k-1"},"Policy":` + mustQuote(t, polB) + `}`

	rootID := upsertOne(t, st, attrsA, `{}`, "scan-A")
	ensureTestScan(t, st, "scan-B")
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-vers-1",
		AttributesJSON: attrsB,
		TagsJSON:       sp(`{}`),
		DiscoveredBy:   "scan-B",
	}); err != nil {
		t.Fatalf("reordered-policy upsert: %v", err)
	}

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("reordered embedded policy must NOT split: got %d rows", len(versions))
	}
}

// TestVersioning_ChangedEmbeddedPolicy_Split is the negative-space companion:
// a genuinely different embedded policy still version-splits (canonicalization
// only absorbs key ordering, never semantic content).
func TestVersioning_ChangedEmbeddedPolicy_Split(t *testing.T) {
	st := openTestStore(t)

	const polA = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kms:Decrypt"}]}`
	const polB = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kms:Decrypt"},{"Effect":"Deny","Action":"kms:Encrypt"}]}`
	attrsA := `{"Policy":` + mustQuote(t, polA) + `}`
	attrsB := `{"Policy":` + mustQuote(t, polB) + `}`

	rootID := upsertOne(t, st, attrsA, `{}`, "scan-A")
	ensureTestScan(t, st, "scan-B")
	if _, err := st.UpsertResource(&Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-vers-1",
		AttributesJSON: attrsB,
		TagsJSON:       sp(`{}`),
		DiscoveredBy:   "scan-B",
	}); err != nil {
		t.Fatalf("changed-policy upsert: %v", err)
	}

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("changed embedded policy must split: got %d rows", len(versions))
	}
}

// TestWithUpsertCounters_SplitsNewVsChanged guards Fix 2: the scoped
// new/changed counters attribute first-discoveries vs version splits so the
// scan progress line can report them separately.
func TestWithUpsertCounters_SplitsNewVsChanged(t *testing.T) {
	st := openTestStore(t)
	ensureTestScan(t, st, "scan-A")
	ensureTestScan(t, st, "scan-B")
	ensureTestScan(t, st, "scan-C")

	rows := []*Resource{
		{Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-1", AttributesJSON: `{"a":1}`, DiscoveredBy: "scan-A"},
		{Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-2", AttributesJSON: `{"a":1}`, DiscoveredBy: "scan-A"},
	}

	// First scan: two brand-new resources → new=2, changed=0.
	var newC, changedC atomic.Int64
	if _, err := st.WithUpsertCounters(&newC, &changedC).UpsertResources(rows); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if newC.Load() != 2 || changedC.Load() != 0 {
		t.Fatalf("first scan: new=%d changed=%d, want new=2 changed=0", newC.Load(), changedC.Load())
	}

	// Second scan: i-1 changes, i-2 unchanged → new=0, changed=1.
	newC.Store(0)
	changedC.Store(0)
	rows2 := []*Resource{
		{Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-1", AttributesJSON: `{"a":2}`, DiscoveredBy: "scan-B"},
		{Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-2", AttributesJSON: `{"a":1}`, DiscoveredBy: "scan-B"},
	}
	if _, err := st.WithUpsertCounters(&newC, &changedC).UpsertResources(rows2); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if newC.Load() != 0 || changedC.Load() != 1 {
		t.Fatalf("second scan: new=%d changed=%d, want new=0 changed=1", newC.Load(), changedC.Load())
	}

	// Third scan: all unchanged → new=0, changed=0 (verify-only).
	newC.Store(0)
	changedC.Store(0)
	rows3 := []*Resource{
		{Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-1", AttributesJSON: `{"a":2}`, DiscoveredBy: "scan-C"},
		{Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-2", AttributesJSON: `{"a":1}`, DiscoveredBy: "scan-C"},
	}
	if _, err := st.WithUpsertCounters(&newC, &changedC).UpsertResources(rows3); err != nil {
		t.Fatalf("third upsert: %v", err)
	}
	if newC.Load() != 0 || changedC.Load() != 0 {
		t.Fatalf("third scan: new=%d changed=%d, want new=0 changed=0", newC.Load(), changedC.Load())
	}
}

// TestVersioning_ListReturnsCurrentRowOnly guards the partial-unique
// invariant: after a version split, ListResources returns one row per
// natural key.
func TestVersioning_ListReturnsCurrentRowOnly(t *testing.T) {
	st := openTestStore(t)
	_ = upsertOne(t, st, `{"a":1}`, `{}`, "scan-A")
	_ = upsertOne(t, st, `{"a":2}`, `{}`, "scan-B")
	_ = upsertOne(t, st, `{"a":3}`, `{}`, "scan-C")

	results, err := st.ListResources(ResourceFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 current row, got %d", len(results))
	}
	if results[0].AttributesJSON != `{"a":3}` {
		t.Errorf("want latest attributes, got %q", results[0].AttributesJSON)
	}
}

// TestVersioning_TypeChange_SupersedesNotForks locks the re-key invariant: a
// resource whose type string changes between scans (same provider/account/
// native_id, even with identical attributes) supersedes its prior row instead
// of forking a second current row. Before the re-key, type was part of the
// identity hash, so a scanner rename orphaned the old current row — two live
// rows for one ARN, the bug this fixes.
func TestVersioning_TypeChange_SupersedesNotForks(t *testing.T) {
	st := openTestStore(t)
	ensureTestScan(t, st, "scan-A")
	r1 := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:instance", NativeID: "i-rekey-1",
		AttributesJSON: `{"a":1}`, DiscoveredBy: "scan-A",
	}
	if _, err := st.UpsertResource(r1); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	rootID := r1.ID

	// Same native_id + identical attrs, but a renamed type.
	ensureTestScan(t, st, "scan-B")
	r2 := &Resource{
		Provider: "aws", AccountID: "acct", Type: "aws:ec2:vpc", NativeID: "i-rekey-1",
		AttributesJSON: `{"a":1}`, DiscoveredBy: "scan-B",
	}
	if _, err := st.UpsertResource(r2); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if r2.ID != rootID {
		t.Errorf("root_id must be type-independent: got %q want %q", r2.ID, rootID)
	}

	// Exactly one current row, carrying the new type — supersede, not fork.
	current, err := st.ListResources(ResourceFilter{Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(current) != 1 {
		t.Fatalf("want exactly 1 current row (supersede), got %d", len(current))
	}
	if current[0].Type != "aws:ec2:vpc" {
		t.Errorf("current row type: got %q want aws:ec2:vpc", current[0].Type)
	}

	// The chain has 2 rows; the old (original-type) row is superseded.
	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("want 2-row chain, got %d", len(versions))
	}
	if versions[0].SupersededBy == nil {
		t.Error("old row must be superseded")
	}
	if versions[0].Type != "aws:ec2:instance" {
		t.Errorf("old row keeps original type: got %q", versions[0].Type)
	}
	if versions[1].Type != "aws:ec2:vpc" {
		t.Errorf("current chain row type: got %q want aws:ec2:vpc", versions[1].Type)
	}
}

// TestNativeIDCollisionDetector_WarnsOnTwoTypesOneNativeID exercises the scan-
// time safety net: two distinct types upserted at one (provider, account,
// native_id) in the same scan run fire exactly one ScanWarning (they would
// otherwise silently share a version chain). Mirrors the real GCP
// iam:policy vs binaryauthorization:policy collision the re-key surfaced.
func TestNativeIDCollisionDetector_WarnsOnTwoTypesOneNativeID(t *testing.T) {
	st := openTestStore(t)
	var warns []ScanWarning
	st.OnWarn = func(w ScanWarning) { warns = append(warns, w) }
	ensureTestScan(t, st, "scan-A")

	mk := func(typ string) *Resource {
		return &Resource{
			Provider: "gcp", AccountID: "proj", Type: typ,
			NativeID: "projects/proj/policy", AttributesJSON: `{}`, DiscoveredBy: "scan-A",
		}
	}
	if _, err := st.UpsertResource(mk("gcp:iam:policy")); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("first upsert must not warn, got %v", warns)
	}
	if _, err := st.UpsertResource(mk("gcp:binaryauthorization:policy")); err != nil {
		t.Fatalf("upsert b: %v", err)
	}
	if len(warns) != 1 {
		t.Fatalf("collision must warn exactly once, got %d: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0].Message, "projects/proj/policy") {
		t.Errorf("warning should name the colliding native_id, got %q", warns[0].Message)
	}
}
