package aws

import (
	"context"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	eventstypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	smithymw "github.com/aws/smithy-go/middleware"
	"github.com/icearp/disco-cli/internal/util"
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
	insertTestScan(t, st, testScanID, "running")
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
	t.Cleanup(func() { assertContainsEdgesDeclared(t, st) })
	return st
}

// declaredContainsEdges is the set of every EdgeDecl the registered resolvers
// declare. Lazy rather than a package var because package vars initialise
// before the init() funcs that populate the registry; the registry is fixed
// once they have run.
var declaredContainsEdges = sync.OnceValue(func() map[EdgeDecl]bool {
	declared := map[EdgeDecl]bool{}
	for _, d := range CollectResolverEdges() {
		declared[d] = true
	}
	return declared
})

// assertContainsEdgesDeclared fails the test for every contains edge in st
// whose (parent type, child type) no registered resolver declares as an
// EdgeDecl{Parent, Child, store.RelContains}. Scanners write these edges via
// RecordHierarchyBatch (directly or through childFanOut), which carries no
// type information, so an undeclared pair is otherwise invisible to
// `disco coverage resolvers`.
//
// It proves only that the pairs some test stores are declared — nothing about
// pairs no test stores. RecordHierarchyBatch writes the contains row only when
// both endpoint rows exist, so a scanner test that never seeds the parent
// leaves its pair unchecked.
func assertContainsEdgesDeclared(t *testing.T, st *store.Store) {
	t.Helper()
	rels, err := st.ListRelationships()
	if err != nil {
		t.Errorf("ListRelationships: %v", err)
		return
	}
	var contains []store.Relationship
	for _, r := range rels {
		if r.Kind == store.RelContains {
			contains = append(contains, r)
		}
	}
	if len(contains) == 0 {
		return
	}
	rows, err := st.ListResources(store.ResourceFilter{IncludeManaged: true, Limit: util.AllResources})
	if err != nil {
		t.Errorf("ListResources: %v", err)
		return
	}
	typeOf := make(map[string]string, len(rows))
	for _, r := range rows {
		typeOf[r.ID] = r.Type
	}
	declared := declaredContainsEdges()
	for _, r := range contains {
		parent, pok := typeOf[r.FromID]
		child, cok := typeOf[r.ToID]
		if !pok || !cok {
			t.Errorf("contains edge %s→%s: endpoint row missing (parent found=%v, child found=%v)", r.FromID, r.ToID, pok, cok)
			continue
		}
		if d := (EdgeDecl{parent, child, store.RelContains}); !declared[d] {
			t.Errorf("undeclared contains edge %s → %s: register EdgeDecl{Parent, Child, store.RelContains} on a resolver (see noopIoTSWHierarchy)", d.Source, d.Target)
		}
	}
}

// insertTestScan inserts a scan record so resources can name it in
// discovered_by — e.g. a second, older scan whose rows must be treated as stale.
func insertTestScan(t *testing.T, st *store.Store, scanID, status string) {
	t.Helper()
	if _, err := st.DB().Exec(`INSERT INTO scans (id, started_at, status, providers, scope) VALUES (?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), ?, '["test"]', '{}')`, scanID, status); err != nil {
		t.Fatalf("insert test scan %s: %v", scanID, err)
	}
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
// scanRegions defaults to us-east-1, so a test that calls a region-electing
// scanner (dispatchedRegions) in another region must set it.
func newTestAccount(id string) *account {
	return &account{ID: id, Name: "Test Account", Regions: []string{"us-east-1"}, scanRegions: []string{"us-east-1"}}
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

// parentCalls records the parent ids a handler was called with.
type parentCalls struct {
	mu  sync.Mutex
	ids []string
}

func (c *parentCalls) add(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ids = append(c.ids, id)
}

// distinct returns the sorted distinct ids (a two-page parent is called twice).
func (c *parentCalls) distinct() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, id := range c.ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// rowsByNativeID returns the stored rows of typ, managed ones included, keyed
// by NativeID.
func rowsByNativeID(t *testing.T, st *store.Store, typ string) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, Types: []string{typ}, IncludeManaged: true, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("ListResources %s: %v", typ, err)
	}
	byID := map[string]store.Resource{}
	for _, r := range rows {
		byID[r.NativeID] = r
	}
	return byID
}

// resourceNativeIDs is the NativeIDs of rows a helper returned, in order.
func resourceNativeIDs(rows []*store.Resource) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.NativeID)
	}
	return ids
}

// assertContainsNative fails the test unless the parent contains the child,
// both given by NativeID.
func assertContainsNative(t *testing.T, st *store.Store, parentNativeID, childNativeID string) {
	t.Helper()
	parentID := store.ResourceID("aws", testAccountID, parentNativeID)
	rels, err := st.RelationshipsFrom(parentID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, parentID, store.ResourceID("aws", testAccountID, childNativeID), store.RelContains)
}

// countOpWarnings counts the scan warnings recorded for op.
func countOpWarnings(st *store.Store, op string) *int {
	var (
		mu sync.Mutex
		n  int
	)
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service == op {
			mu.Lock()
			n++
			mu.Unlock()
		}
	}
	return &n
}

// recordCallRegions makes cfg record the region of every call it answers. The
// recorder goes ahead of cfg's own stubs, which short-circuit the stack.
func recordCallRegions(cfg sdkaws.Config, regions *parentCalls) sdkaws.Config {
	record := func(s *smithymw.Stack) error {
		return s.Initialize.Add(smithymw.InitializeMiddlewareFunc("discoRecordRegion",
			func(ctx context.Context, in smithymw.InitializeInput, next smithymw.InitializeHandler) (smithymw.InitializeOutput, smithymw.Metadata, error) {
				regions.add(awsmw.GetRegion(ctx))
				return next.HandleInitialize(ctx, in)
			}), smithymw.After)
	}
	cfg.APIOptions = append([]func(*smithymw.Stack) error{record}, cfg.APIOptions...)
	return cfg
}

// seedParentRows stores a row for each NativeID, so the contains rows the
// fan-out records have both endpoints.
func seedParentRows(t *testing.T, st *store.Store, typ string, nativeIDs ...string) {
	t.Helper()
	seeds := make([]*store.Resource, 0, len(nativeIDs))
	for _, id := range nativeIDs {
		seeds = append(seeds, &store.Resource{Provider: "aws", AccountID: testAccountID, Type: typ, NativeID: id, DiscoveredBy: testScanID})
	}
	if _, err := st.UpsertResources(seeds); err != nil {
		t.Fatalf("seed %s: %v", typ, err)
	}
}
