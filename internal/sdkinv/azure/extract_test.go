package azure

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

func TestExtract_Fixture(t *testing.T) {
	u, err := extractor{}.Extract(context.Background(), filepath.Join("testdata", "cache"))
	if err != nil {
		t.Fatal(err)
	}
	got := byKey(u)
	want := map[string]struct {
		class  sdkinv.Class
		depth  int
		parent string
		scope  sdkinv.Scope
		ops    int
	}{
		"microsoft.widgets/widgets":              {sdkinv.ClassResource, 0, "", sdkinv.ScopeSubscription, 2},
		"microsoft.widgets/widgets/parts":        {sdkinv.ClassResource, 1, "microsoft.widgets/widgets", sdkinv.ScopeResourceGroup, 1},
		"microsoft.widgets/widgets/instanceview": {sdkinv.ClassNonResource, 1, "microsoft.widgets/widgets", sdkinv.ScopeResourceGroup, 1},
		"microsoft.widgets/skus":                 {sdkinv.ClassCatalog, 0, "", sdkinv.ScopeSubscription, 1},
		"microsoft.widgets/operations":           {sdkinv.ClassNonResource, 0, "", sdkinv.ScopeTenant, 1},
		"microsoft.widgets/tenantthings":         {sdkinv.ClassResource, 0, "", sdkinv.ScopeTenant, 1},
		"microsoft.widgets/roleassignments":      {sdkinv.ClassResource, 0, "", sdkinv.ScopeExtension, 1},
	}
	if len(got) != len(want) {
		t.Errorf("candidates = %d, want %d: %v", len(got), len(want), keys(got))
	}
	for k, w := range want {
		c, ok := got[k]
		if !ok {
			t.Errorf("missing candidate %s", k)
			continue
		}
		if c.Class != w.class || c.Depth != w.depth || c.Parent != w.parent || len(c.Ops) != w.ops || c.Ops[0].Scope != w.scope {
			t.Errorf("%s = class %s depth %d parent %q scope %s ops %d; want %+v", k, c.Class, c.Depth, c.Parent, c.Ops[0].Scope, len(c.Ops), w)
		}
	}
	w := got["microsoft.widgets/widgets"]
	// Refs: sub-resource structs and ID-suffixed strings below the envelope,
	// through nested properties; the envelope's own ID/Name/Type are not refs.
	if want := []string{"properties.extra.policyID", "properties.subnetID", "properties.vault"}; !slices.Equal(w.Refs, want) {
		t.Errorf("widgets refs = %v, want %v", w.Refs, want)
	}
	if w.Ops[0].Label != "armwidgets:Widgets.List" || !w.Ops[0].Paged || w.Ops[0].Path == "" {
		t.Errorf("widgets op = %+v", w.Ops[0])
	}
	if p := got["microsoft.widgets/widgets/parts"]; p.Ops[0].Paged || len(p.Ops[0].Targets) != 1 || p.Ops[0].Targets[0] != "widgets" {
		t.Errorf("parts op = %+v", p.Ops[0])
	}
	// checkNameAvailability is POST-only: never a candidate, but an Other op.
	if _, ok := got["microsoft.widgets/checknameavailability"]; ok {
		t.Error("POST-only action path became a candidate")
	}
	others := map[string]bool{}
	for _, o := range u.Other {
		others[o.Label] = true
	}
	if !others["armwidgets:Widgets.Get"] || !others["armwidgets:TenantThings.CheckName"] || others["armwidgets:Widgets.List"] {
		t.Errorf("Other = %v", others)
	}
}

func keys(m map[string]sdkinv.Candidate) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestExtract_Live runs against the real fetched cache when present and pins
// a handful of anchors the plan promised.
// TestExtract_Accounting: an exported method with no builder is dropped with
// a reason, and an exported method whose name ends in CreateRequest is not
// mistaken for a builder of a phantom operation.
func TestExtract_Accounting(t *testing.T) {
	u, err := extractor{}.Extract(context.Background(), filepath.Join("testdata", "cache"))
	if err != nil {
		t.Fatal(err)
	}
	var drops []string
	for _, d := range u.Dropped {
		drops = append(drops, d.Op.Name+"="+d.Reason)
	}
	if want := []string{"TenantThingsClient.Rename=no-request-builder", "WidgetsClient.ValidateWidgetCreateRequest=no-request-builder"}; !reflect.DeepEqual(drops, want) {
		t.Errorf("dropped = %v, want %v", drops, want)
	}
	for _, r := range u.SourceOps {
		if r.Name == "WidgetsClient.ValidateWidget" {
			t.Errorf("phantom builder op %v", r)
		}
	}
}

func TestExtract_Live(t *testing.T) {
	dir := sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}.Dir("azure", sdkinv.AzureSDKRef)
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
		"microsoft.compute/virtualmachines":            {sdkinv.ClassResource, 0, sdkinv.ScopeSubscription},
		"microsoft.compute/virtualmachines/extensions": {sdkinv.ClassResource, 1, sdkinv.ScopeResourceGroup},
		"microsoft.compute/skus":                       {sdkinv.ClassNonResource, 0, sdkinv.ScopeSubscription},
		"microsoft.compute/operations":                 {sdkinv.ClassNonResource, 0, sdkinv.ScopeTenant},
		"microsoft.storage/storageaccounts":            {sdkinv.ClassResource, 0, sdkinv.ScopeSubscription},
		"microsoft.resources/resourcegroups":           {sdkinv.ClassResource, 0, sdkinv.ScopeSubscription},
		"microsoft.authorization/roleassignments":      {sdkinv.ClassResource, 0, sdkinv.ScopeExtension},
		"microsoft.management/managementgroups":        {sdkinv.ClassResource, 0, sdkinv.ScopeTenant},
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
	counts := map[sdkinv.Class]int{}
	for _, c := range u.Candidates {
		counts[c.Class]++
	}
	t.Logf("azure candidates: %d (%v), diagnostics %d", len(u.Candidates), counts, len(u.Diagnostics))
	if counts[sdkinv.ClassResource] < 800 {
		t.Errorf("resource candidates = %d, expected several hundred", counts[sdkinv.ClassResource])
	}
}
