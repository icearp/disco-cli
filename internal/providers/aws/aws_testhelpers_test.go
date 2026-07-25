package aws

import (
	"path/filepath"
	"testing"

	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	eventstypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// testScanID is the fixed scan ID inserted into every test database.
	testScanID = "00000000000000000000000000000000"
	// testAccountID and testRegion are canonical values shared across resolver tests.
	testAccountID = "123456789012"
	testRegion    = "us-east-1"
)

// newTestStore opens a temp SQLite DB for provider tests and inserts a scan
// record so resources can satisfy the discovered_by FK.
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
	// Direction invariant: every `contains` edge must flow parent→child.
	// Cleanup fails the test if a reversed row sneaks in — guards direction
	// regressions that would otherwise pass silently, since
	// UpsertRelationship's UNIQUE on (from, to, kind) no-ops the second insert.
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

// upsertTestResource inserts a minimal resource with the given AttributesJSON
// and returns its computed stable ID. Pass an empty region to leave Region unset.
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

// upsertTestResourceNamed is upsertTestResource with an explicit Name set, for
// resolvers that build a name-keyed index (account fixed to testAccountID).
func upsertTestResourceNamed(t *testing.T, st *store.Store, rtype, nativeID, region, attrsJSON, name string) string {
	t.Helper()
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      testAccountID,
		Type:           rtype,
		NativeID:       nativeID,
		Name:           &name,
		AttributesJSON: attrsJSON,
		DiscoveredBy:   testScanID,
	}
	if region != "" {
		r.Region = &region
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsertTestResourceNamed %s/%s: %v", rtype, nativeID, err)
	}
	return store.ResourceID("aws", testAccountID, nativeID)
}

// newTestAccount returns a minimal account struct for use in resolver tests.
func newTestAccount(id string) *account {
	return &account{ID: id, Name: "Test Account", Regions: []string{"us-east-1"}}
}

// assertRelationship fails the test if no relationship with the given
// (from, to, kind) exists in the rels slice.
func assertRelationship(t *testing.T, rels []store.Relationship, fromID, toID, kind string) {
	t.Helper()
	for _, r := range rels {
		if r.FromID == fromID && r.ToID == toID && r.Kind == kind {
			return
		}
	}
	t.Errorf("missing relationship: %s -[%s]-> %s", fromID, kind, toID)
}

// Helpers build resolver-test AttributesJSON from real SDK structs, so drift
// in scanner-side wrapping shape (added/removed key, renamed field, SDK type
// change) surfaces here rather than as silent zero-value resolutions in
// production.
//
// Each helper mirrors a wrapper container declared inline in the matching
// scanner file. Those wrapper structs are unexported function-local types
// and can't be reused directly, so these helpers reproduce the shape with
// anonymous structs sharing the same json tags.

// elbv2LBAttrs reproduces the wrapping shape produced by elb_scanners.go:
// {"lb": <SDK LoadBalancer>, "type": "<lb-type>"}.
func elbv2LBAttrs(lb elbv2types.LoadBalancer) string {
	return mustJSON(map[string]any{"lb": lb, "type": string(lb.Type)})
}

// elbv2TargetGroupAttrs reproduces tgWithTargets in elb_scanners.go:
// {"TargetGroup": <SDK TargetGroup>, "Targets": [<SDK TargetDescription>...]}.
func elbv2TargetGroupAttrs(tg elbv2types.TargetGroup, targets ...elbv2types.TargetDescription) string {
	return mustJSON(struct {
		TargetGroup elbv2types.TargetGroup         `json:"TargetGroup"`
		Targets     []elbv2types.TargetDescription `json:"Targets"`
	}{TargetGroup: tg, Targets: targets})
}

// eventBridgeRuleAttrs reproduces ruleWithTargets in eventbridge_scanners.go:
// {"Rule": <SDK Rule>, "Targets": [<SDK Target>...]}.
func eventBridgeRuleAttrs(rule eventstypes.Rule, targets ...eventstypes.Target) string {
	return mustJSON(struct {
		Rule    eventstypes.Rule     `json:"Rule"`
		Targets []eventstypes.Target `json:"Targets"`
	}{Rule: rule, Targets: targets})
}
