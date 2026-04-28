package rules

import (
	"path/filepath"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// newTestStore opens a temp DB and creates a scan row so Resource FKs hold.
func newTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	scanID, err := db.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	return db, scanID
}

// TestEvaluate_EBSUnencrypted seeds two volumes — one encrypted, one not —
// and asserts only the unencrypted one is flagged by the builtin rule.
func TestEvaluate_EBSUnencrypted(t *testing.T) {
	st, scanID := newTestStore(t)
	enc := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
		NativeID: "vol-a", AttributesJSON: `{"Encrypted": true}`, DiscoveredBy: scanID,
	}
	unenc := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
		NativeID: "vol-b", AttributesJSON: `{"Encrypted": false}`, DiscoveredBy: scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{enc, unenc}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	rs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	findings, err := Evaluate(st, filterID(rs, "aws-ebs-unencrypted"), "")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings: got %d, want 1", len(findings))
	}
	if findings[0].ResourceID != unenc.ID {
		t.Errorf("wrong resource flagged: %s (want %s)", findings[0].ResourceID, unenc.ID)
	}
}

// TestEvaluate_SeverityFloor checks minSev skips low-severity rules.
func TestEvaluate_SeverityFloor(t *testing.T) {
	st, scanID := newTestStore(t)
	r := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:volume",
		NativeID: "vol-a", AttributesJSON: `{"Encrypted": false}`, DiscoveredBy: scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{r}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	bi, _ := Builtins()
	// aws-ebs-unencrypted is medium; minSev=critical should suppress it.
	findings, err := Evaluate(st, filterID(bi, "aws-ebs-unencrypted"), SevCritical)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings under critical floor, got %d", len(findings))
	}
}

// TestEvaluate_PredicateOps covers each op against a single resource.
func TestEvaluate_PredicateOps(t *testing.T) {
	st, scanID := newTestStore(t)
	r := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:iam:access-key",
		NativeID: "akid-1",
		AttributesJSON: `{"Status":"Active","CreateDate":"2021-05-01T00:00:00Z",
			"Tags":[{"Key":"env","Value":"prod"}]}`,
		DiscoveredBy: scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{r}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	cases := []struct {
		name string
		pred Predicate
		hit  bool
	}{
		{"eq-string-match", Predicate{Path: "Status", Op: "eq", Value: "Active"}, true},
		{"eq-string-miss", Predicate{Path: "Status", Op: "eq", Value: "Inactive"}, false},
		{"ne", Predicate{Path: "Status", Op: "ne", Value: "Inactive"}, true},
		{"exists", Predicate{Path: "Status", Op: "exists"}, true},
		{"not_exists-missing", Predicate{Path: "DoesNotExist", Op: "not_exists"}, true},
		{"not_exists-present", Predicate{Path: "Status", Op: "not_exists"}, false},
		{"contains-array", Predicate{Path: "Tags.#.Value", Op: "contains", Value: "prod"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evalPredicate(tc.pred, r.AttributesJSON)
			if got != tc.hit {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.hit)
			}
		})
	}
}

// filterID narrows rules to a single id for focused testing.
func filterID(rs []Rule, id string) []Rule {
	for _, r := range rs {
		if r.ID == id {
			return []Rule{r}
		}
	}
	return nil
}

// TestEvaluate_RelatedTraversal verifies a rule with Related fires only when
// the resource has an outbound edge to a target matching the inner Match.
// Models: instance -[uses]-> SG (open). Without the open SG, no finding.
func TestEvaluate_RelatedTraversal(t *testing.T) {
	st, scanID := newTestStore(t)
	openSG := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:security-group",
		NativeID: "sg-open", AttributesJSON: `{"GroupName":"open"}`, DiscoveredBy: scanID,
	}
	closedSG := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:security-group",
		NativeID: "sg-closed", AttributesJSON: `{"GroupName":"closed"}`, DiscoveredBy: scanID,
	}
	openInst := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:instance",
		NativeID: "i-open", AttributesJSON: `{}`, DiscoveredBy: scanID,
	}
	safeInst := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:instance",
		NativeID: "i-safe", AttributesJSON: `{}`, DiscoveredBy: scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{openSG, closedSG, openInst, safeInst}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.UpsertRelationship(openInst.ID, openSG.ID, store.RelUses, "directed", nil); err != nil {
		t.Fatalf("rel open: %v", err)
	}
	if err := st.UpsertRelationship(safeInst.ID, closedSG.ID, store.RelUses, "directed", nil); err != nil {
		t.Fatalf("rel safe: %v", err)
	}

	rule := Rule{
		ID:       "instance-with-open-sg",
		Severity: SevHigh,
		Match: Match{
			Type: "aws:ec2:instance",
			Related: &RelatedMatch{
				Direction: "out",
				Kinds:     []string{store.RelUses},
				Target: Match{
					Type: "aws:ec2:security-group",
					Where: []Predicate{
						{Path: "GroupName", Op: "eq", Value: "open"},
					},
				},
			},
		},
	}
	findings, err := Evaluate(st, []Rule{rule}, "")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings: got %d, want 1", len(findings))
	}
	if findings[0].ResourceID != openInst.ID {
		t.Errorf("wrong resource flagged: got %s want %s", findings[0].ResourceID, openInst.ID)
	}
}

// TestEvaluate_RelatedDirection verifies "in" direction walks inbound edges.
func TestEvaluate_RelatedDirection(t *testing.T) {
	st, scanID := newTestStore(t)
	parent := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:vpc",
		NativeID: "vpc-1", AttributesJSON: `{}`, DiscoveredBy: scanID,
	}
	child := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:subnet",
		NativeID: "sub-1", AttributesJSON: `{"AvailabilityZone":"us-east-1a"}`, DiscoveredBy: scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{parent, child}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.UpsertRelationship(child.ID, parent.ID, store.RelAttachedTo, "directed", nil); err != nil {
		t.Fatalf("rel: %v", err)
	}
	rule := Rule{
		ID:       "vpc-with-az-subnet",
		Severity: SevLow,
		Match: Match{
			Type: "aws:ec2:vpc",
			Related: &RelatedMatch{
				Direction: "in",
				Kinds:     []string{store.RelAttachedTo},
				Target: Match{
					Type: "aws:ec2:subnet",
					Where: []Predicate{
						{Path: "AvailabilityZone", Op: "eq", Value: "us-east-1a"},
					},
				},
			},
		},
	}
	findings, err := Evaluate(st, []Rule{rule}, "")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(findings) != 1 || findings[0].ResourceID != parent.ID {
		t.Errorf("expected 1 finding on %s, got %+v", parent.ID, findings)
	}
}
