package gcpinventory

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

// opByLabel is the candidate's op with that label.
func opByLabel(c sdkinv.Candidate, label string) sdkinv.Operation {
	for _, op := range c.Ops {
		if op.Label == label {
			return op
		}
	}
	return sdkinv.Operation{}
}

// scopes is the candidate's distinct op scopes, sorted and space-joined.
func scopes(c sdkinv.Candidate) string {
	var out []string
	for _, op := range c.Ops {
		out = append(out, string(op.Scope))
	}
	slices.Sort(out)
	return strings.Join(slices.Compact(out), " ")
}

func TestExtract_Fixture(t *testing.T) {
	u, err := extractor{}.Extract(context.Background(), filepath.Join("testdata", "cache"))
	if err != nil {
		t.Fatal(err)
	}
	if missing, extra, conflicts := sdkinv.Unaccounted(u); len(missing)+len(extra)+len(conflicts) > 0 {
		t.Errorf("unaccounted ops: missing %v extra %v conflicts %v", missing, extra, conflicts)
	}
	got := byKey(u)
	// Each row pins one structural rule; the fixture comments say which.
	want := map[string]struct {
		class  sdkinv.Class
		rule   string
		depth  int
		parent string
		scopes string
	}{
		"widgets/widgets":                     {sdkinv.ClassResource, "create", 0, "", "global project"},
		"widgets/widgets/parts":               {sdkinv.ClassResource, "create", 1, "widgets/widgets", "project"},
		"widgets/widgets/revisions":           {sdkinv.ClassCatalog, "get-only", 1, "widgets/widgets", "project"},
		"widgets/widgets/hubs":                {sdkinv.ClassCatalog, "get-only", 1, "widgets/widgets", "project"},
		"widgets/operations":                  {sdkinv.ClassNonResource, "operation-node", 0, "", "project"},
		"widgets/globaloperations":            {sdkinv.ClassNonResource, "operation-node", 0, "", "project"},
		"widgets/changes":                     {sdkinv.ClassNonResource, "operation-node", 0, "", "project"},
		"widgets/snapshots":                   {sdkinv.ClassResource, "created-elsewhere", 0, "", "project"},
		"widgets/connectors":                  {sdkinv.ClassResource, "mutable", 0, "", "project"},
		"widgets/groups":                      {sdkinv.ClassResource, "mutable", 0, "", "project"},
		"widgets/hosts":                       {sdkinv.ClassCatalog, "get-only", 0, "", "project"},
		"widgets/jobs":                        {sdkinv.ClassResource, "create", 0, "", "project"},
		"widgets/aliases":                     {sdkinv.ClassResource, "create", 0, "", "project"},
		"widgets/blocks":                      {sdkinv.ClassCatalog, "get-only", 0, "", "project"},
		"widgets/assets":                      {sdkinv.ClassResource, "delete-only", 0, "", "unscoped"},
		"widgets/cogs":                        {sdkinv.ClassResource, "create", 0, "", "unscoped"},
		"widgets/zones":                       {sdkinv.ClassCatalog, "get-only", 0, "", "project"},
		"widgets/gizmos":                      {sdkinv.ClassResource, "create", 0, "", "project"},
		"widgets/regiongizmos":                {sdkinv.ClassResource, "create", 0, "", "project"},
		"widgets/organizationgizmos":          {sdkinv.ClassCatalog, "get-only", 0, "", "org"},
		"widgets/sprocketpools":               {sdkinv.ClassResource, "create", 0, "", "project"},
		"widgets/lakes":                       {sdkinv.ClassResource, "create", 0, "", "project"},
		"widgets/lakes/zones":                 {sdkinv.ClassResource, "create", 1, "widgets/lakes", "project"},
		"widgets/lakes/zones/assets":          {sdkinv.ClassResource, "create", 2, "widgets/lakes/zones", "project"},
		"widgets/hubs":                        {sdkinv.ClassResource, "create", 0, "", "project"},
		"widgets/topics":                      {sdkinv.ClassResource, "create", 0, "", "project"},
		"widgets/policies":                    {sdkinv.ClassResource, "create", 0, "", "project"},
		"widgets/gears":                       {sdkinv.ClassResource, "create", 0, "", "global project"},
		"widgets/namespaces/gears":            {sdkinv.ClassCatalog, "get-only", 1, "", "global"},
		"widgets/namespaces/zones":            {sdkinv.ClassCatalog, "get-only", 1, "", "global"},
		"widgets/billingaccounts":             {sdkinv.ClassResource, "create", 0, "", "billing-account"},
		"widgets/billingaccounts/subaccounts": {sdkinv.ClassResource, "create", 1, "widgets/billingaccounts", "billing-account"},
		"widgets/organizations":               {sdkinv.ClassCatalog, "get-only", 0, "", "org"},
		"widgets/buckets":                     {sdkinv.ClassResource, "create", 0, "", "project"},
		"widgets/notifications":               {sdkinv.ClassResource, "create", 1, "widgets/buckets", "global"},
		"widgets/gadgets":                     {sdkinv.ClassResource, "create", 0, "", "project"},
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
		if c.Class != w.class || c.Rule != w.rule || c.Depth != w.depth || c.Parent != w.parent || scopes(c) != w.scopes {
			t.Errorf("%s = class %s rule %s depth %d parent %q scopes %q signals %v; want %+v", k, c.Class, c.Rule, c.Depth, c.Parent, scopes(c), c.Signals, w)
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
	// v1, v1beta1, and v1beta1's namespace route folded across documents.
	if c := got["widgets/widgets"]; hasSignal(c, "preview-only") || len(c.Ops) != 3 || opByLabel(c, "widgets:namespaces.widgets.list").Label == "" {
		t.Errorf("widgets (v1+v1beta1) = %v ops %+v", c.Signals, c.Ops)
	}
	if c := got["widgets/gizmos"]; len(c.Ops) != 2 || c.Ops[0].Label != "widgets:gizmos.aggregatedList" {
		t.Errorf("gizmos ops = %+v", c.Ops)
	}
	if c := got["widgets/regiongizmos"]; !hasSignal(c, "aggregated-by:gizmos") || len(c.Ops) != 2 {
		t.Errorf("regiongizmos = %v ops %d", c.Signals, len(c.Ops))
	}
	// A project's aggregated list never returns the org-scoped twin.
	if c := got["widgets/organizationgizmos"]; len(c.Ops) != 1 {
		t.Errorf("organizationgizmos ops = %+v", c.Ops)
	}
	if c := got["widgets/jobs"]; !hasSignal(c, "create-verb") {
		t.Errorf("jobs signals = %v", c.Signals)
	}
	// run v1's namespace route folds into the top-level route to its element.
	if c := got["widgets/gears"]; len(c.Ops) != 2 || c.Ops[0].Label != "widgets:namespaces.gears.list" {
		t.Errorf("gears ops = %+v", c.Ops)
	}
	if op := opByLabel(got["widgets/widgets"], "widgets:projects.locations.widgets.list"); op.Label != "widgets:projects.locations.widgets.list" || !op.Paged || !slices.Equal(op.Required, []string{"parent"}) {
		t.Errorf("widgets op = %+v", op)
	}
	if _, ok := got["tube/channels"]; ok {
		t.Error("non-cloud API tube included")
	}
	others := map[string]bool{}
	for _, o := range u.Other {
		others[o.Label] = true
	}
	// A filtered view over another schema and a paged GET on an item path
	// are not listers.
	for _, l := range []string{"widgets:projects.locations.widgets.get", "widgets:projects.locations.widgets.searchSprockets", "widgets:projects.locations.widgets.parts.fetch"} {
		if !others[l] {
			t.Errorf("%s not in Other", l)
		}
	}
	if others["widgets:projects.locations.widgets.list"] || others["tube:channels.get"] {
		t.Errorf("Other = %v", others)
	}
	drops := map[string]string{}
	for _, d := range u.Dropped {
		drops[d.Op.Label] = d.Reason
	}
	if drops["tube:channels.list"] != "non-cloud-api" || drops["widgetsadmin:projects.locations.widgets.list"] != "alias-document" {
		t.Errorf("Dropped = %v", drops)
	}
	var excluded, unscoped bool
	for _, d := range u.Diagnostics {
		excluded = excluded || strings.Contains(d.Message, "cloud-platform") && strings.HasSuffix(d.Message, ": tube")
		unscoped = unscoped || d.Severity == "warn" && strings.HasSuffix(d.Message, ": widgets:assets.list widgets:cogs.list")
	}
	if !excluded || !unscoped {
		t.Errorf("diagnostics: excluded %v unscoped %v: %+v", excluded, unscoped, u.Diagnostics)
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
		class  sdkinv.Class
		depth  int
		parent string
		scope  sdkinv.Scope
	}{
		"compute/instances":             {sdkinv.ClassResource, 0, "", ScopeProject},
		"compute/zones":                 {sdkinv.ClassCatalog, 0, "", ScopeProject},
		"compute/machinetypes":          {sdkinv.ClassCatalog, 0, "", ScopeProject},
		"compute/globaloperations":      {sdkinv.ClassNonResource, 0, "", ScopeProject},
		"compute/regiondisks":           {sdkinv.ClassResource, 0, "", ScopeProject},
		"run/jobs/executions":           {sdkinv.ClassResource, 1, "run/jobs", ScopeProject},
		"storage/buckets":               {sdkinv.ClassResource, 0, "", ScopeProject},
		"storage/notifications":         {sdkinv.ClassResource, 1, "storage/buckets", ScopeGlobal},
		"sqladmin/databases":            {sdkinv.ClassResource, 1, "sqladmin/instances", ScopeProject},
		"cloudkms/keyrings/cryptokeys":  {sdkinv.ClassResource, 1, "cloudkms/keyrings", ScopeProject},
		"cloudresourcemanager/projects": {sdkinv.ClassResource, 0, "", ScopeProject},
		"container/clusters":            {sdkinv.ClassResource, 0, "", ScopeProject},
		"cloudidentity/groups":          {sdkinv.ClassResource, 0, "", ScopeGlobal},
		"admin/users":                   {sdkinv.ClassResource, 0, "", ScopeGlobal},
		// An organization can be patched and undeleted, so it is not the
		// provider's read-only catalog (#44).
		"cloudresourcemanager/organizations": {sdkinv.ClassResource, 0, "", ScopeOrg},
		"monitoring/alertpolicies":           {sdkinv.ClassResource, 0, "", ScopeProject},
		// Found by the item GET the old name rules missed (L1).
		"sqladmin/backups": {sdkinv.ClassResource, 0, "", ScopeProject},
		// Parents matched by item path, not by the nearest key prefix (H20).
		"compute/regioninstancegroupmanagerresizerequests": {sdkinv.ClassResource, 1, "compute/regioninstancegroupmanagers", ScopeProject},
		"compute/regionmultimigmembers":                    {sdkinv.ClassCatalog, 1, "compute/regionmultimigs", ScopeProject},
		// run v1's namespace route folds into v2's collection.
		"run/workerpools": {sdkinv.ClassResource, 0, "", ScopeProject},
		// A tenancy root the API creates is a parent.
		"cloudbilling/billingaccounts/subaccounts": {sdkinv.ClassResource, 1, "cloudbilling/billingaccounts", ScopeBillingAccount},
	}
	for k, w := range anchors {
		c, ok := got[k]
		if !ok {
			t.Errorf("missing %s", k)
			continue
		}
		// A live collection reached through several versions and routes may
		// carry several scopes; the anchor names the one it must have.
		if c.Class != w.class || c.Depth != w.depth || c.Parent != w.parent || !slices.Contains(strings.Fields(scopes(c)), string(w.scope)) {
			t.Errorf("%s = class %s depth %d parent %q scopes %q signals %v; want %+v", k, c.Class, c.Depth, c.Parent, scopes(c), c.Signals, w)
		}
	}
	for _, k := range []string{"youtube/channels", "adsense/accounts", "dfareporting/userprofiles", "chromepolicy/policies", "run/namespaces/jobs"} {
		if _, ok := got[k]; ok {
			t.Errorf("candidate present: %s", k)
		}
	}
	// Every Parent names a candidate key: the contract README states and
	// conformance asserts on fixtures. Live, the document tree nests nodes
	// that list nothing (appengine/apps), so a dangling pointer means the
	// walk-up in Extract stopped resolving.
	for _, c := range u.Candidates {
		if c.Parent != "" && got[c.Parent].Key == "" {
			t.Errorf("%s: parent %q is not a candidate key", c.Key, c.Parent)
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
