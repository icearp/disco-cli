package gcp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

func hasSignal(c sdkinv.Candidate, s string) bool {
	for _, x := range c.Signals {
		if x == s {
			return true
		}
	}
	return false
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
	}{
		"widgets/widgets":       {sdkinv.ClassResource, 0, "", sdkinv.ScopeProject},
		"widgets/widgets/parts": {sdkinv.ClassResource, 1, "widgets/widgets", sdkinv.ScopeProject},
		"widgets/operations":    {sdkinv.ClassNonResource, 0, "", sdkinv.ScopeProject},
		"widgets/zones":         {sdkinv.ClassCatalog, 0, "", sdkinv.ScopeProject},
		"widgets/gizmos":        {sdkinv.ClassResource, 0, "", sdkinv.ScopeProject},
		"widgets/buckets":       {sdkinv.ClassResource, 0, "", sdkinv.ScopeGlobal},
		"widgets/notifications": {sdkinv.ClassResource, 1, "widgets/buckets", sdkinv.ScopeGlobal},
		"widgets/gadgets":       {sdkinv.ClassResource, 0, "", sdkinv.ScopeProject},
	}
	if len(got) != len(want) {
		t.Errorf("candidates = %d, want %d", len(got), len(want))
		for k := range got {
			t.Log(k)
		}
	}
	for k, w := range want {
		c, ok := got[k]
		if !ok {
			t.Errorf("missing %s", k)
			continue
		}
		if c.Class != w.class || c.Depth != w.depth || c.Parent != w.parent || c.Ops[0].Scope != w.scope {
			t.Errorf("%s = class %s depth %d parent %q scope %s signals %v; want %+v", k, c.Class, c.Depth, c.Parent, c.Ops[0].Scope, c.Signals, w)
		}
	}
	if c := got["widgets/gadgets"]; !hasSignal(c, "preview-only") {
		t.Errorf("gadgets (v1beta1 only) lacks preview-only: %v", c.Signals)
	}
	// Refs: strings the schema describes as URLs, resource names or
	// accounts, through one nested schema; name/selfLink are the element's own.
	if want := []string{"network", "serviceAccount", "sizing.machineType"}; !slices.Equal(got["widgets/widgets"].Refs, want) {
		t.Errorf("widgets refs = %v, want %v", got["widgets/widgets"].Refs, want)
	}
	if c := got["widgets/widgets"]; hasSignal(c, "preview-only") || len(c.Ops) != 2 {
		t.Errorf("widgets (v1+v1beta1) = %v ops %d", c.Signals, len(c.Ops))
	}
	if c := got["widgets/gizmos"]; len(c.Ops) != 2 || c.Ops[0].Label != "widgets:gizmos.aggregatedList" {
		t.Errorf("gizmos ops = %+v", c.Ops)
	}
	if c := got["widgets/widgets"]; c.Ops[0].Label != "widgets:projects.locations.widgets.list" || !c.Ops[0].Paged {
		t.Errorf("widgets op = %+v", c.Ops[0])
	}
	if _, ok := got["tube/channels"]; ok {
		t.Error("non-cloud API tube included")
	}
	others := map[string]bool{}
	for _, o := range u.Other {
		others[o.Label] = true
	}
	if !others["widgets:projects.locations.widgets.get"] || others["widgets:projects.locations.widgets.list"] || others["tube:channels.get"] {
		t.Errorf("Other = %v", others)
	}
	found := false
	for _, d := range u.Diagnostics {
		found = found || strings.Contains(d.Message, "non-cloud APIs excluded: tube")
	}
	if !found {
		t.Errorf("no exclusion diagnostic: %+v", u.Diagnostics)
	}
}

func TestExtract_Live(t *testing.T) {
	e := extractor{}
	dir := sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}.Dir("gcp", e.Ref())
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Skip("gcp SDK cache not fetched; run: disco coverage sdk fetch --providers gcp")
	}
	u, err := e.Extract(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got := byKey(u)
	anchors := map[string]struct {
		class sdkinv.Class
		depth int
		scope sdkinv.Scope
	}{
		"compute/instances":                  {sdkinv.ClassResource, 0, sdkinv.ScopeProject},
		"compute/zones":                      {sdkinv.ClassCatalog, 0, sdkinv.ScopeProject},
		"compute/machinetypes":               {sdkinv.ClassCatalog, 0, sdkinv.ScopeProject},
		"compute/globaloperations":           {sdkinv.ClassNonResource, 0, sdkinv.ScopeProject},
		"compute/regiondisks":                {sdkinv.ClassResource, 0, sdkinv.ScopeProject},
		"run/jobs/executions":                {sdkinv.ClassResource, 1, sdkinv.ScopeProject},
		"storage/buckets":                    {sdkinv.ClassResource, 0, sdkinv.ScopeGlobal},
		"storage/notifications":              {sdkinv.ClassResource, 1, sdkinv.ScopeGlobal},
		"sqladmin/databases":                 {sdkinv.ClassResource, 1, sdkinv.ScopeProject},
		"cloudkms/keyrings/cryptokeys":       {sdkinv.ClassResource, 1, sdkinv.ScopeProject},
		"cloudresourcemanager/projects":      {sdkinv.ClassResource, 0, sdkinv.ScopeProject},
		"container/clusters":                 {sdkinv.ClassResource, 0, sdkinv.ScopeProject},
		"cloudidentity/groups":               {sdkinv.ClassResource, 0, sdkinv.ScopeGlobal},
		"admin/users":                        {sdkinv.ClassResource, 0, sdkinv.ScopeGlobal},
		"cloudresourcemanager/organizations": {sdkinv.ClassCatalog, 0, sdkinv.ScopeOrg},
		"monitoring/alertpolicies":           {sdkinv.ClassResource, 0, sdkinv.ScopeProject},
	}
	for k, w := range anchors {
		c, ok := got[k]
		if !ok {
			t.Errorf("missing %s", k)
			continue
		}
		if c.Class != w.class || c.Depth != w.depth || c.Ops[0].Scope != w.scope {
			t.Errorf("%s = class %s depth %d parent %q scope %s signals %v; want %+v", k, c.Class, c.Depth, c.Parent, c.Ops[0].Scope, c.Signals, w)
		}
	}
	for _, k := range []string{"youtube/channels", "adsense/accounts", "dfareporting/userprofiles"} {
		if _, ok := got[k]; ok {
			t.Errorf("non-cloud API candidate present: %s", k)
		}
	}
	counts := map[sdkinv.Class]int{}
	apis := map[string]bool{}
	for _, c := range u.Candidates {
		counts[c.Class]++
		apis[c.Service] = true
	}
	t.Logf("gcp candidates: %d (%v) across %d APIs; diagnostics %d", len(u.Candidates), counts, len(apis), len(u.Diagnostics))
	if counts[sdkinv.ClassResource] < 500 || len(apis) < 100 {
		t.Errorf("universe too small: %v across %d APIs", counts, len(apis))
	}
}
