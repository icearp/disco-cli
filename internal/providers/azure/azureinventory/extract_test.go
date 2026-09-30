package azureinventory

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

func byKey(u *sdkinv.Universe) map[string]sdkinv.Candidate {
	m := make(map[string]sdkinv.Candidate, len(u.Candidates))
	for _, c := range u.Candidates {
		m[c.Key] = c
	}
	return m
}

func fixtureUniverse(t *testing.T) *sdkinv.Universe {
	t.Helper()
	u, err := extractor{}.Extract(context.Background(), filepath.Join("testdata", "cache"))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func opNames(c sdkinv.Candidate) []string {
	var out []string
	for _, o := range c.Ops {
		out = append(out, o.Name)
	}
	sort.Strings(out)
	return out
}

// TestExtract_Fixture pins one candidate per rule; the fixture's comments
// (testdata/.../armwidgets) name the rule each client exercises.
func TestExtract_Fixture(t *testing.T) {
	u := fixtureUniverse(t)
	got := byKey(u)
	type want struct {
		class  sdkinv.Class
		rule   string
		depth  int
		parent string
		scope  sdkinv.Scope
		ops    []string
	}
	w := func(class sdkinv.Class, rule string, depth int, parent string, scope sdkinv.Scope, ops ...string) want {
		return want{class, rule, depth, parent, scope, ops}
	}
	const res, cat, non = sdkinv.ClassResource, sdkinv.ClassCatalog, sdkinv.ClassNonResource
	wants := map[string]want{
		"microsoft.widgets/widgets": w(res, "item-write", 0, "", ScopeSubscription, "WidgetsClient.List", "WidgetsClient.ListByResourceGroup"),
		// The response-array decode shape, plus an alternate lister of the
		// same element folded in from its unwritten path.
		"microsoft.widgets/widgets/parts":   w(res, "item-write", 1, "microsoft.widgets/widgets", ScopeResourceGroup, "WidgetPartsClient.List", "WidgetPartsClient.ListAll"),
		"microsoft.widgets/tenantthings":    w(res, "item-write", 0, "", ScopeTenant, "TenantThingsClient.List"),
		"microsoft.widgets/skus":            w(cat, "item-read-only", 0, "", ScopeSubscription, "SKUsClient.List"),
		"microsoft.widgets/operations":      w(non, "no-item-path", 0, "", ScopeTenant, "OperationsClient.List"),
		"microsoft.widgets/roleassignments": w(res, "item-write", 0, "", ScopeExtension, "RoleAssignmentsClient.ListForScope"),
		// A polymorphic element resolves to its base, which carries the envelope.
		"microsoft.widgets/gizmos": w(res, "arm-envelope", 0, "", ScopeSubscription, "GizmosClient.List"),
		// The envelope at tenant scope and no parent is the provider's catalog.
		"microsoft.widgets/publishedthings": w(cat, "item-read-only", 0, "", ScopeTenant, "PublishedThingsClient.List"),
		// configs/web is written: web is an id, so configs is a resource and the
		// snapshots below web key through it.
		"microsoft.widgets/widgets/configs":               w(res, "item-write", 1, "microsoft.widgets/widgets", ScopeResourceGroup, "ConfigsClient.ListConfigs"),
		"microsoft.widgets/widgets/configs/snapshots":     w(res, "arm-envelope", 2, "microsoft.widgets/widgets/configs", ScopeResourceGroup, "ConfigsClient.ListSnapshots"),
		"microsoft.widgets/widgets/settings/rules":        w(res, "item-write", 2, "microsoft.widgets/widgets/settings", ScopeResourceGroup, "SettingsClient.ListRules"),
		"microsoft.widgets/widgets/settings":              w(cat, "item-read-only", 1, "microsoft.widgets/widgets", ScopeResourceGroup, "SettingsClient.List"),
		"microsoft.widgets/accounts":                      w(cat, "item-read-only", 0, "", ScopeTenant, "AccountsClient.List"),
		"microsoft.widgets/accounts/invoices":             w(cat, "item-read-only", 1, "microsoft.widgets/accounts", ScopeTenant, "AccountsClient.ListInvoices"),
		"microsoft.widgets/accounts/deleted":              w(non, "no-item-path", 0, "", ScopeTenant, "AccountsClient.ListDeleted"),
		"microsoft.widgets/widgets/draft/testjob/streams": w(cat, "item-read-only", 1, "microsoft.widgets/widgets", ScopeResourceGroup, "DraftsClient.ListStreams"), // parent: the innermost id-bearing static
		"microsoft.resources/deploymentthings":            w(res, "item-write", 0, "", ScopeResourceGroup, "DeploymentThingsClient.List"),
		"microsoft.widgets/usages":                        w(non, "no-item-path", 0, "", ScopeSubscription, "UsagesClient.List"),
		"microsoft.widgets/features":                      w(non, "no-item-path", 0, "", ScopeSubscription, "FeaturesClient.List"),
		"microsoft.widgets/attachments":                   w(res, "item-write", 0, "", ScopeExtension, "AttachmentsClient.List"),
		"microsoft.widgets/budgets":                       w(res, "item-write", 0, "", ScopeManagementGroup, "BudgetsClient.List"),
		// Its one GET answers one partner, yet nothing else lists the written collection.
		"microsoft.widgets/partners": w(res, "item-write", 0, "", ScopeTenant, "PartnersClient.Get"),
		"microsoft.widgets/quotas":   w(res, "arm-envelope", 0, "", ScopeSubscription, "QuotasClient.List", "QuotasClient.ListBySubscription"),
		// The envelope, but reached only through a location: a regional catalog.
		"microsoft.widgets/versions": w(cat, "item-read-only", 0, "", ScopeSubscription, "VersionsClient.List"),
		// gadgetry/armwidgets shares the armwidgets basename and op names.
		"microsoft.gadgetry/widgets": w(non, "no-item-path", 0, "", ScopeSubscription, "WidgetsClient.List"),
	}
	var extra []string
	for k := range got {
		if _, ok := wants[k]; !ok {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		t.Errorf("unexpected candidates %v", extra)
	}
	for k, w := range wants {
		c, ok := got[k]
		if !ok {
			t.Errorf("missing candidate %s", k)
			continue
		}
		g := want{c.Class, c.Rule, c.Depth, c.Parent, c.Ops[0].Scope, opNames(c)}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("%s = %+v, want %+v", k, g, w)
		}
	}
	for _, s := range []struct {
		key, signal string
		want        bool
	}{
		{"microsoft.widgets/usages", LocationScopeSignal, true},
		{"microsoft.widgets/versions", LocationScopeSignal, true},
		{"microsoft.widgets/widgets", LocationScopeSignal, false},
		{"microsoft.widgets/quotas", LocationScopeSignal, false}, // a subscription lister too
		{"microsoft.widgets/widgets/parts", "alternate-lister", true},
		{"microsoft.widgets/partners", "single-answer-lister", true},
		{"microsoft.widgets/accounts", "item:POST", false}, // checkName is an action, not an id
	} {
		if c := got[s.key]; slices.Contains(c.Signals, s.signal) != s.want {
			t.Errorf("%s signals = %v, want %s: %v", s.key, c.Signals, s.signal, s.want)
		}
	}

	wd := got["microsoft.widgets/widgets"]
	if wd.Ops[0].Label != "armwidgets:Widgets.List" || !wd.Ops[0].Paged || wd.Ops[0].Path == "" || wd.Ops[0].Client != "WidgetsClient" {
		t.Errorf("widgets op = %+v", wd.Ops[0])
	}
	if p := got["microsoft.widgets/widgets/parts"]; p.Ops[0].Name != "WidgetPartsClient.List" || p.Ops[0].Paged || !reflect.DeepEqual(p.Ops[0].Targets, []string{"widgets"}) {
		t.Errorf("parts op = %+v", p.Ops[0])
	}

	others := map[string]bool{}
	for _, o := range u.Other {
		others[o.Label] = true
	}
	for _, l := range []string{
		"armwidgets:Widgets.Get", "armwidgets:TenantThings.CheckName", "armwidgets:Accounts.CheckName",
		"armwidgets:Widgets.InstanceView", "armwidgets:Widgets.Validate", // singleton reads, not listers
		"armwidgets:Configs.UpdateWeb", "armwidgets:Settings.GetDefault",
		"armwidgets:ProviderTypes.List", "armwidgets:Client.GetByID", // unattributable
	} {
		if !others[l] {
			t.Errorf("Other lacks %s", l)
		}
	}
	if others["armwidgets:Widgets.List"] {
		t.Error("a lister is also in Other")
	}
	var diags []string
	for _, d := range u.Diagnostics {
		diags = append(diags, d.Message)
	}
	if want := []string{
		"GET /subscriptions/{subscriptionId}/providers/{resourceProviderNamespace}/resourceTypes: namespace is a parameter",
		"GET /{resourceId}: no resource type after the scopes",
	}; !reflect.DeepEqual(diags, want) {
		t.Errorf("diagnostics = %v, want %v", diags, want)
	}
}

// TestExtract_Refs: refs by shape, named by the serde wire names.
func TestExtract_Refs(t *testing.T) {
	w := byKey(fixtureUniverse(t))["microsoft.widgets/widgets"]
	want := []string{
		"properties.deep.level2.shallowId",  // depth 3 is still a ref...
		"properties.encryption.keyUrl",      // a URI beside a sub-resource; wire name from a struct tag
		"properties.encryption.sourceVault", // sub-resource struct
		"properties.extra.policyId",         // wire name from UnmarshalJSON alone
		"properties.image.galleryImageId",   // a three-field struct with an ID is walked, not a ref
		"properties.image.id",
		"properties.subnetId", // wire name from MarshalJSON, not lowerFirst's subnetID
		"properties.vault",
	}
	// Not refs: the element's own id; extra.endpointUri (a URI with no
	// sub-resource sibling); hiddenId (no wire name); level3.targetId (below
	// refDepth); links (a map).
	if !slices.Equal(w.Refs, want) {
		t.Errorf("widgets refs = %v\nwant %v", w.Refs, want)
	}
}

// TestExtract_Accounting: every exported method is a source op, and one with
// no builder, or whose builder has no literal path, is dropped with a reason.
func TestExtract_Accounting(t *testing.T) {
	u := fixtureUniverse(t)
	var drops []string
	for _, d := range u.Dropped {
		drops = append(drops, d.Op.Name+"="+d.Reason)
	}
	want := []string{
		"TenantThingsClient.Rename=no-request-builder",
		"WidgetsClient.GetStream=no-request-path",
		"WidgetsClient.ValidateWidgetCreateRequest=no-request-builder",
	}
	if !reflect.DeepEqual(drops, want) {
		t.Errorf("dropped = %v, want %v", drops, want)
	}
	if missing, extra, conflicts := sdkinv.Unaccounted(u); len(missing)+len(extra)+len(conflicts) > 0 {
		t.Errorf("unaccounted: missing %v extra %v conflicts %v", missing, extra, conflicts)
	}
	for _, r := range u.SourceOps {
		if r.Name == "WidgetsClient.ValidateWidget" {
			t.Errorf("phantom builder op %v", r)
		}
	}
	// A Begin wrapper reaches its builder through an unexported method.
	if !slices.ContainsFunc(u.SourceOps, func(r sdkinv.OpRef) bool { return r.Name == "WidgetsClient.CreateOrUpdate" }) {
		t.Error("BeginCreateOrUpdate did not reach createOrUpdateCreateRequest")
	}
}

func TestAbsentModules(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "compute", "armcompute"), 0o755); err != nil {
		t.Fatal(err)
	}
	deps := []*debug.Module{
		{Path: armPrefix + "compute/armcompute/v6", Version: "v6.4.0"},      // present: the major dir is not the module dir
		{Path: armPrefix + "appplatform/armappplatform", Version: "v1.2.0"}, // retired upstream
		{Path: "github.com/Azure/azure-sdk-for-go/sdk/azcore", Version: "v1.0.0"},
	}
	got := absentModules(root, deps)
	if len(got) != 1 || got[0].Severity != "warn" || !strings.Contains(got[0].Message, "appplatform/armappplatform") {
		t.Errorf("absentModules = %+v", got)
	}
}

func TestExtract_SkipsUnparseableModule(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo", "sdk", "resourcemanager")
	if err := os.CopyFS(root, os.DirFS(filepath.Join("testdata", "cache", "repo", "sdk", "resourcemanager"))); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(root, "broken", "armbroken")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "client.go"), []byte("package armbroken\nfunc (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	u, err := extractor{}.Extract(context.Background(), filepath.Dir(filepath.Dir(filepath.Dir(root))))
	if err != nil {
		t.Fatalf("one unparseable module aborted the extraction: %v", err)
	}
	if len(u.Candidates) != len(fixtureUniverse(t).Candidates) {
		t.Errorf("candidates = %d, want the fixture's %d", len(u.Candidates), len(fixtureUniverse(t).Candidates))
	}
	if !slices.ContainsFunc(u.Diagnostics, func(d sdkinv.Diagnostic) bool {
		return d.Severity == "warn" && d.Source == "broken/armbroken"
	}) {
		t.Errorf("no warn diagnostic names broken/armbroken: %+v", u.Diagnostics)
	}
}

// TestExtract_Live runs against the real fetched cache when present and pins
// a handful of anchors the plan promised.
func TestExtract_Live(t *testing.T) {
	dir := sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}.Dir("azure", SDKRef)
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Skip("azure SDK cache not fetched; run: disco coverage sdk fetch --providers azure")
	}
	u, err := extractor{}.Extract(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := byKey(u)
	anchors := map[string]struct {
		class sdkinv.Class
		depth int
		scope sdkinv.Scope
	}{
		"microsoft.compute/virtualmachines":            {sdkinv.ClassResource, 0, ScopeSubscription},
		"microsoft.compute/virtualmachines/extensions": {sdkinv.ClassResource, 1, ScopeResourceGroup},
		"microsoft.compute/skus":                       {sdkinv.ClassNonResource, 0, ScopeSubscription},
		"microsoft.compute/operations":                 {sdkinv.ClassNonResource, 0, ScopeTenant},
		"microsoft.storage/storageaccounts":            {sdkinv.ClassResource, 0, ScopeSubscription},
		"microsoft.resources/resourcegroups":           {sdkinv.ClassResource, 0, ScopeSubscription},
		"microsoft.authorization/roleassignments":      {sdkinv.ClassResource, 0, ScopeExtension},
		"microsoft.management/managementgroups":        {sdkinv.ClassResource, 0, ScopeTenant},
		// P2: config/web is written, so web is an id.
		"microsoft.web/sites/config":           {sdkinv.ClassResource, 1, ScopeResourceGroup},
		"microsoft.web/sites/config/snapshots": {sdkinv.ClassResource, 2, ScopeResourceGroup},
		// blobServices/default is the one blob service: default is an id.
		"microsoft.storage/storageaccounts/blobservices/containers": {sdkinv.ClassResource, 2, ScopeResourceGroup},
		// L3: armdeployments' own paths are microsoft.resources.
		"microsoft.resources/deployments": {sdkinv.ClassResource, 0, ScopeExtension},
	}
	for k, w := range anchors {
		c, ok := got[k]
		if !ok {
			t.Errorf("missing %s", k)
			continue
		}
		if c.Class != w.class || c.Depth != w.depth || c.Ops[0].Scope != w.scope {
			t.Errorf("%s = class %s depth %d scope %s signals %v; want %+v", k, c.Class, c.Depth, c.Ops[0].Scope, c.Signals, w)
		}
	}
	if c, ok := got["microsoft.resources/resources"]; ok && c.Class == sdkinv.ClassResource {
		t.Errorf("generic /subscriptions/{id}/resources lister classified as a resource: %v", c.Signals)
	}
	if missing, extra, conflicts := sdkinv.Unaccounted(u); len(missing)+len(extra)+len(conflicts) > 0 {
		t.Errorf("unaccounted: %d missing, %d extra, %d conflicts", len(missing), len(extra), len(conflicts))
	}
	counts := map[sdkinv.Class]int{}
	for _, c := range u.Candidates {
		counts[c.Class]++
	}
	t.Logf("azure candidates: %d (%v), diagnostics %d", len(u.Candidates), counts, len(u.Diagnostics))
	if counts[sdkinv.ClassResource] < 800 {
		t.Errorf("resource candidates = %d, expected several hundred", counts[sdkinv.ClassResource])
	}
}
