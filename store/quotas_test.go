package store

import (
	"encoding/json"
	"testing"
)

func strptr(s string) *string   { return &s }
func fltptr(f float64) *float64 { return &f }
func intptr(i int) *int         { return &i }

// newQuota builds a minimal valid quota for the test scan.
func newQuota(serviceCode, quotaCode string, value float64) *Quota {
	return &Quota{
		Provider:     "aws",
		AccountID:    "111122223333",
		Region:       "us-east-1",
		ServiceCode:  serviceCode,
		QuotaCode:    quotaCode,
		Name:         "Running On-Demand Standard instances",
		Unit:         strptr("None"),
		Value:        fltptr(value),
		Adjustable:   true,
		DiscoveredBy: testScanID,
	}
}

// currentRows counts live rows for one natural key, and total rows in its chain.
func currentRows(t *testing.T, st *Store, rootID string) (live, total int) {
	t.Helper()
	if err := st.get(&live, `SELECT count(*) FROM quotas WHERE root_id = ? AND superseded_by IS NULL`, rootID); err != nil {
		t.Fatalf("count live: %v", err)
	}
	if err := st.get(&total, `SELECT count(*) FROM quotas WHERE root_id = ?`, rootID); err != nil {
		t.Fatalf("count total: %v", err)
	}
	return live, total
}

func TestUpsertQuotas_FirstDiscoveryThenUnchangedRescan(t *testing.T) {
	st := openTestStore(t)

	q := newQuota("ec2", "L-1216C47A", 5)
	n, err := st.UpsertQuotas([]*Quota{q})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if n != 1 {
		t.Fatalf("first upsert inserted %d rows, want 1", n)
	}

	// Rescanning an unchanged quota must not grow the chain. This is the
	// property that makes a catalogue of this size safe to scan repeatedly.
	n, err = st.UpsertQuotas([]*Quota{newQuota("ec2", "L-1216C47A", 5)})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if n != 0 {
		t.Fatalf("unchanged rescan inserted %d rows, want 0", n)
	}

	live, total := currentRows(t, st, q.ID)
	if live != 1 || total != 1 {
		t.Fatalf("after unchanged rescan: live=%d total=%d, want 1 and 1", live, total)
	}
}

func TestUpsertQuotas_ValueChangeSplitsChain(t *testing.T) {
	st := openTestStore(t)

	if _, err := st.UpsertQuotas([]*Quota{newQuota("ec2", "L-1216C47A", 5)}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	raised := newQuota("ec2", "L-1216C47A", 64)
	n, err := st.UpsertQuotas([]*Quota{raised})
	if err != nil {
		t.Fatalf("changed upsert: %v", err)
	}
	if n != 1 {
		t.Fatalf("value change inserted %d rows, want 1", n)
	}

	live, total := currentRows(t, st, raised.ID)
	if live != 1 {
		t.Fatalf("live rows = %d after split, want exactly 1", live)
	}
	if total != 2 {
		t.Fatalf("chain length = %d after split, want 2", total)
	}

	cur, err := st.GetQuota(raised.ID)
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if cur == nil || cur.Value == nil || *cur.Value != 64 {
		t.Fatalf("current value = %v, want 64", cur)
	}

	versions, err := st.GetQuotaVersions(raised.ID)
	if err != nil {
		t.Fatalf("GetQuotaVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("history length = %d, want 2", len(versions))
	}
	if versions[0].Value == nil || *versions[0].Value != 5 {
		t.Fatalf("oldest version value = %v, want 5", versions[0].Value)
	}
	if versions[0].SupersededBy == nil {
		t.Fatal("oldest version has no superseded_by; the chain is not linked")
	}
	if versions[1].SupersededBy != nil {
		t.Fatal("newest version is superseded; it should be the live row")
	}
	if versions[1].PreviousVersionID == nil || *versions[1].PreviousVersionID != versions[0].VersionRowID {
		t.Fatal("newest version does not point back at its predecessor")
	}
	// discovered_at/by are inherited from the chain root, so "first seen" is
	// stable across splits.
	if versions[1].DiscoveredAt != versions[0].DiscoveredAt {
		t.Fatalf("discovered_at drifted across the split: %q then %q",
			versions[0].DiscoveredAt, versions[1].DiscoveredAt)
	}
}

// A provider rewording a quota label is not a limit change. If display columns
// split the chain, a single upstream copy edit manufactures ~100k versions.
func TestUpsertQuotas_DisplayOnlyChangeDoesNotSplit(t *testing.T) {
	st := openTestStore(t)

	if _, err := st.UpsertQuotas([]*Quota{newQuota("ec2", "L-1216C47A", 5)}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	reworded := newQuota("ec2", "L-1216C47A", 5)
	reworded.Name = "Running On-Demand Standard (A, C, D, H, I, M, R, T, Z) instances"
	reworded.ServiceName = strptr("Amazon Elastic Compute Cloud (Amazon EC2)")
	reworded.Description = strptr("The maximum number of vCPUs for On-Demand Standard instances.")
	n, err := st.UpsertQuotas([]*Quota{reworded})
	if err != nil {
		t.Fatalf("reworded upsert: %v", err)
	}
	if n != 0 {
		t.Fatalf("display-only change inserted %d rows, want 0", n)
	}

	_, total := currentRows(t, st, reworded.ID)
	if total != 1 {
		t.Fatalf("chain length = %d after a display-only change, want 1", total)
	}
	cur, err := st.GetQuota(reworded.ID)
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if cur.Name != reworded.Name {
		t.Fatalf("name did not update in place: %q", cur.Name)
	}
	if cur.ServiceName == nil || *cur.ServiceName != *reworded.ServiceName {
		t.Fatalf("service_name did not update in place: %v", cur.ServiceName)
	}
	// A description is prose the provider can reword at will. If it split the
	// chain, one upstream copy edit would version every quota in the catalogue.
	if cur.Description == nil || *cur.Description != *reworded.Description {
		t.Fatalf("description did not update in place: %v", cur.Description)
	}
}

// A non-adjustable limit the provider moves is the highest-value signal in this
// dataset, so it must produce a version exactly like an adjustable one does.
func TestUpsertQuotas_NonAdjustableProviderChangeIsRecorded(t *testing.T) {
	st := openTestStore(t)

	hard := newQuota("lambda", "L-B99A9384", 1000)
	hard.Adjustable = false
	hard.DefaultValue = fltptr(1000)
	if _, err := st.UpsertQuotas([]*Quota{hard}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	moved := newQuota("lambda", "L-B99A9384", 4000)
	moved.Adjustable = false
	moved.DefaultValue = fltptr(4000)
	if _, err := st.UpsertQuotas([]*Quota{moved}); err != nil {
		t.Fatalf("moved upsert: %v", err)
	}

	versions, err := st.GetQuotaVersions(moved.ID)
	if err != nil {
		t.Fatalf("GetQuotaVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("history length = %d, want 2", len(versions))
	}
	if versions[0].Adjustable || versions[1].Adjustable {
		t.Fatal("adjustable flag did not round-trip as false")
	}
}

// NULL and a value are distinct limits. A provider that starts reporting a
// default where it previously reported none has told us something.
func TestUpsertQuotas_NullToValueSplits(t *testing.T) {
	st := openTestStore(t)

	q := newQuota("ec2", "L-1216C47A", 5)
	q.DefaultValue = nil
	if _, err := st.UpsertQuotas([]*Quota{q}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	withDefault := newQuota("ec2", "L-1216C47A", 5)
	withDefault.DefaultValue = fltptr(5)
	n, err := st.UpsertQuotas([]*Quota{withDefault})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if n != 1 {
		t.Fatalf("NULL to value inserted %d rows, want 1", n)
	}
}

// The region is part of the identity: the same service and quota code carry
// genuinely different limits per region, and collapsing them would make each
// region supersede the last on every scan.
func TestQuotaID_RegionIsPartOfIdentity(t *testing.T) {
	east := QuotaID("aws", "111122223333", "us-east-1", "ec2", "L-1216C47A", "")
	west := QuotaID("aws", "111122223333", "us-west-2", "ec2", "L-1216C47A", "")
	if east == west {
		t.Fatal("QuotaID collapses two regions onto one identity")
	}

	st := openTestStore(t)
	a := newQuota("ec2", "L-1216C47A", 5)
	b := newQuota("ec2", "L-1216C47A", 64)
	b.Region = "us-west-2"
	n, err := st.UpsertQuotas([]*Quota{a, b})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if n != 2 {
		t.Fatalf("two regions inserted %d rows, want 2", n)
	}
}

// An empty dimension key must hash exactly as the five-part key did before the
// column existed. Every quota row stored to date carries one, so this is what
// lets the multi-cloud reshape ship as pure DDL: break it and every root_id
// moves, every version chain is orphaned, and no migration puts them back.
//
// The expected value is the literal pre-019 hash rather than a recomputation,
// so a change to the append rule fails here instead of silently re-keying a
// live table.
func TestQuotaID_EmptyDimensionMatchesThePreDimensionHash(t *testing.T) {
	const preDimension = "f0bb083ce473a6a9b0b1e9e067dad022" // sha256("aws|111122223333|us-east-1|ec2|L-1216C47A")[:16]
	if got := QuotaID("aws", "111122223333", "us-east-1", "ec2", "L-1216C47A", ""); got != preDimension {
		t.Fatalf("QuotaID with an empty dimension = %s, want %s — every stored quota's root_id just moved", got, preDimension)
	}
}

// Two dimension sets under one quota code are two limits, not one row that
// overwrites itself. AWS reports this through QuotaContext and GCP through
// dimensionsInfos.
func TestQuotaID_DimensionIsPartOfIdentity(t *testing.T) {
	bare := QuotaID("aws", "111122223333", "us-east-1", "connect", "L-1", "")
	dim := QuotaID("aws", "111122223333", "us-east-1", "connect", "L-1", "arn:aws:connect:us-east-1:111122223333:instance/abc")
	if bare == dim {
		t.Fatal("QuotaID collapses a dimensioned limit onto its undimensioned identity")
	}

	st := openTestStore(t)
	a := newQuota("connect", "L-1", 5)
	b := newQuota("connect", "L-1", 64)
	b.DimensionKey = "arn:aws:connect:us-east-1:111122223333:instance/abc"
	n, err := st.UpsertQuotas([]*Quota{a, b})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if n != 2 {
		t.Fatalf("two dimensions inserted %d rows, want 2", n)
	}
}

// The whole point of the typed-column comparison: a provider churning a field
// inside the raw payload must not manufacture a version. GCP's Cloud Quotas
// reports a rollout flag that flips mid-rollout and Azure's
// Properties.Properties is an opaque bag, so an attributes-driven comparison
// splits chains for reasons no operator can act on.
func TestUpsertQuotas_AttributeOnlyChangeDoesNotSplit(t *testing.T) {
	st := openTestStore(t)

	first := newQuota("ec2", "L-1216C47A", 5)
	first.AttributesJSON = `{"Value":5,"RolloutInfo":{"OngoingRollout":false}}`
	if _, err := st.UpsertQuotas([]*Quota{first}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	churned := newQuota("ec2", "L-1216C47A", 5)
	churned.AttributesJSON = `{"Value":5,"RolloutInfo":{"OngoingRollout":true}}`
	n, err := st.UpsertQuotas([]*Quota{churned})
	if err != nil {
		t.Fatalf("churned upsert: %v", err)
	}
	if n != 0 {
		t.Fatalf("attribute-only change inserted %d rows, want 0", n)
	}

	// It must not split — and it must not freeze either. attributes no longer
	// decides anything, so a column that neither versions nor updates would
	// keep whatever the provider said the very first time, forever.
	cur, err := st.GetQuota(churned.ID)
	if err != nil {
		t.Fatalf("GetQuota: %v", err)
	}
	if !jsonEqual(cur.AttributesJSON, churned.AttributesJSON) {
		t.Fatalf("attributes did not refresh in place: %s", cur.AttributesJSON)
	}
}

// Each typed column the comparison reads must be able to split a chain on its
// own. Without a case per column, dropping any one of them from unchanged()
// leaves the suite green while that fact silently stops being tracked.
func TestUpsertQuotas_EachSemanticColumnSplits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*Quota)
	}{
		{"value", func(q *Quota) { q.Value = fltptr(64) }},
		{"default_value", func(q *Quota) { q.DefaultValue = fltptr(32) }},
		{"adjustable", func(q *Quota) { q.Adjustable = false }},
		{"unit", func(q *Quota) { q.Unit = strptr("Bytes") }},
		{"period_unit", func(q *Quota) { q.PeriodUnit = strptr("minute") }},
		{"period_value", func(q *Quota) { q.PeriodValue = intptr(5) }},
		{"resource_type", func(q *Quota) { q.ResourceType = strptr("AWS::Connect::Instance") }},
		{"availability_zone", func(q *Quota) { q.AvailabilityZone = strptr("us-east-1b") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t)

			base := newQuota("ec2", "L-1216C47A", 5)
			base.DefaultValue = fltptr(5)
			base.PeriodUnit = strptr("second")
			base.PeriodValue = intptr(1)
			base.ResourceType = strptr("AWS::EC2::Instance")
			base.AvailabilityZone = strptr("us-east-1a")
			if _, err := st.UpsertQuotas([]*Quota{base}); err != nil {
				t.Fatalf("first upsert: %v", err)
			}

			changed := newQuota("ec2", "L-1216C47A", 5)
			changed.DefaultValue = fltptr(5)
			changed.PeriodUnit = strptr("second")
			changed.PeriodValue = intptr(1)
			changed.ResourceType = strptr("AWS::EC2::Instance")
			changed.AvailabilityZone = strptr("us-east-1a")
			tc.apply(changed)

			n, err := st.UpsertQuotas([]*Quota{changed})
			if err != nil {
				t.Fatalf("changed upsert: %v", err)
			}
			if n != 1 {
				t.Fatalf("a %s change inserted %d rows, want 1 — unchanged() no longer reads it", tc.name, n)
			}
			if _, total := currentRows(t, st, changed.ID); total != 2 {
				t.Fatalf("chain length = %d, want 2", total)
			}
		})
	}
}

func TestListQuotas_Filters(t *testing.T) {
	st := openTestStore(t)

	adj := newQuota("ec2", "L-1216C47A", 64)
	adj.DefaultValue = fltptr(5) // raised

	hard := newQuota("lambda", "L-B99A9384", 1000)
	hard.Adjustable = false
	hard.DefaultValue = fltptr(1000) // at default

	other := newQuota("s3", "L-DC2B2D3D", 100)
	other.Region = "eu-west-1"

	if _, err := st.UpsertQuotas([]*Quota{adj, hard, other}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	all, err := st.ListQuotas(QuotaFilter{})
	if err != nil {
		t.Fatalf("ListQuotas: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("unfiltered list returned %d rows, want 3", len(all))
	}

	no := false
	nonAdjustable, err := st.ListQuotas(QuotaFilter{Adjustable: &no})
	if err != nil {
		t.Fatalf("ListQuotas adjustable=false: %v", err)
	}
	if len(nonAdjustable) != 1 || nonAdjustable[0].ServiceCode != "lambda" {
		t.Fatalf("adjustable=false returned %d rows: %+v", len(nonAdjustable), nonAdjustable)
	}

	raised, err := st.ListQuotas(QuotaFilter{RaisedOnly: true})
	if err != nil {
		t.Fatalf("ListQuotas raised: %v", err)
	}
	if len(raised) != 1 || raised[0].ServiceCode != "ec2" {
		t.Fatalf("RaisedOnly returned %d rows: %+v", len(raised), raised)
	}

	byRegion, err := st.ListQuotas(QuotaFilter{Regions: []string{"eu-west-1"}})
	if err != nil {
		t.Fatalf("ListQuotas region: %v", err)
	}
	if len(byRegion) != 1 || byRegion[0].ServiceCode != "s3" {
		t.Fatalf("region filter returned %d rows: %+v", len(byRegion), byRegion)
	}

	byService, err := st.ListQuotas(QuotaFilter{ServiceCodes: []string{"ec2", "s3"}})
	if err != nil {
		t.Fatalf("ListQuotas service: %v", err)
	}
	if len(byService) != 2 {
		t.Fatalf("service filter returned %d rows, want 2", len(byService))
	}
}

// A list projection must carry the stable cross-version identity, not the
// per-version row id — otherwise every id a caller sees changes on each split.
func TestListQuotas_IDIsStableAcrossSplits(t *testing.T) {
	st := openTestStore(t)

	q := newQuota("ec2", "L-1216C47A", 5)
	if _, err := st.UpsertQuotas([]*Quota{q}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	before, err := st.ListQuotas(QuotaFilter{})
	if err != nil {
		t.Fatalf("ListQuotas: %v", err)
	}
	if _, err := st.UpsertQuotas([]*Quota{newQuota("ec2", "L-1216C47A", 64)}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	after, err := st.ListQuotas(QuotaFilter{})
	if err != nil {
		t.Fatalf("ListQuotas: %v", err)
	}
	if len(before) != 1 || len(after) != 1 {
		t.Fatalf("expected one live row before and after, got %d and %d", len(before), len(after))
	}
	if before[0].ID != after[0].ID || after[0].ID != q.ID {
		t.Fatalf("quota id changed across a split: %q then %q (want %q)", before[0].ID, after[0].ID, q.ID)
	}
}

// UpsertQuotas is reachable from disco-saas inside a caller-owned transaction,
// where WrapTx leaves s.db nil. A method reaching for s.db.Begin nil-panics
// there, and a build check cannot see it.
func TestUpsertQuotas_RunsInsideCallerOwnedTx(t *testing.T) {
	st := openTestStore(t)

	tx, err := st.db.Beginx()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	wrapped := WrapTx(tx, DriverSQLite)
	q := newQuota("ec2", "L-1216C47A", 5)
	n, err := wrapped.UpsertQuotas([]*Quota{q})
	if err != nil {
		t.Fatalf("upsert inside caller tx: %v", err)
	}
	if n != 1 {
		t.Fatalf("inserted %d rows inside caller tx, want 1", n)
	}
	// Unchanged rescan on the same transaction still verifies rather than splits.
	n, err = wrapped.UpsertQuotas([]*Quota{newQuota("ec2", "L-1216C47A", 5)})
	if err != nil {
		t.Fatalf("second upsert inside caller tx: %v", err)
	}
	if n != 0 {
		t.Fatalf("unchanged rescan inside caller tx inserted %d rows, want 0", n)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	live, total := currentRows(t, st, q.ID)
	if live != 1 || total != 1 {
		t.Fatalf("after committing caller tx: live=%d total=%d, want 1 and 1", live, total)
	}
}

func TestQuota_JSONRoundTrip(t *testing.T) {
	q := newQuota("ec2", "L-1216C47A", 5)
	q.AttributesJSON = `{"quotaArn":"arn:aws:servicequotas:us-east-1:111122223333:ec2/L-1216C47A"}`

	data, err := q.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Quota
	if err := back.UnmarshalJSON(data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !jsonEqual(back.AttributesJSON, q.AttributesJSON) {
		t.Fatalf("attributes did not round-trip: %q", back.AttributesJSON)
	}
	if back.QuotaCode != q.QuotaCode || back.Value == nil || *back.Value != *q.Value {
		t.Fatalf("scalars did not round-trip: %+v", back)
	}

	// An empty blob must render as an object so consumers can traverse it
	// without a presence check.
	empty := Quota{}
	data, err = empty.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	var probe struct {
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if probe.Attributes == nil {
		t.Fatal("empty attributes rendered as null, want an object")
	}
}
