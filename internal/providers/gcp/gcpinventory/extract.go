package gcpinventory

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// Discovery document subset. Schemas stay raw and decode one at a time on
// the ref walk (refs.go).
type doc struct {
	Name          string                     `json:"name"`
	Version       string                     `json:"version"`
	RootURL       string                     `json:"rootUrl"`
	ServicePath   string                     `json:"servicePath"`
	CanonicalName string                     `json:"canonicalName"`
	Methods       map[string]*method         `json:"methods"`
	Resources     map[string]*resource       `json:"resources"`
	Schemas       map[string]json.RawMessage `json:"schemas"`
}

// identity is what makes two Discovery documents the same service: sql and
// sqladmin both answer at https://sqladmin.googleapis.com/ and duplicate eight
// collections. rootUrl alone is not enough — 31 documents share
// https://www.googleapis.com/.
func (d *doc) identity() string {
	return d.RootURL + "\x00" + d.ServicePath + "\x00" + d.CanonicalName
}

type resource struct {
	Methods   map[string]*method   `json:"methods"`
	Resources map[string]*resource `json:"resources"`
}

type method struct {
	Path       string           `json:"path"`
	FlatPath   string           `json:"flatPath"`
	HTTPMethod string           `json:"httpMethod"`
	Parameters map[string]param `json:"parameters"`
	Response   struct {
		Ref string `json:"$ref"`
	} `json:"response"`
}

type param struct {
	Required    bool   `json:"required"`
	Location    string `json:"location"`
	Pattern     string `json:"pattern"`
	Description string `json:"description"`
}

// altListers enumerate a collection under another name. Narrow on purpose: a
// shape-based rule mints 313 bogus rows from filtered sub-views such as
// listUsable and listManagedInstances.
var altListers = map[string]bool{"search": true, "fetch": true, "listPolicies": true}

// cloudRoots are the tenancy containers Google Cloud / Workspace list within.
// An API belongs to the universe only when at least one lister is rooted in
// one of them; APIs rooted elsewhere (youtube channels, adsense accounts) are
// not cloud infrastructure.
var cloudRoots = map[string]sdkinv.Scope{
	"projects": ScopeProject, "organizations": ScopeOrg, "folders": ScopeFolder,
	"billingaccounts": ScopeBillingAccount, "customers": ScopeTenant, "customer": ScopeTenant,
}

// scopeVocab is the Discovery scope vocabulary, narrowest first.
var scopeVocab = []sdkinv.Scope{
	ScopeProject, ScopeOrg, ScopeFolder, ScopeBillingAccount, ScopeTenant, ScopeGlobal,
}

// scopeNames (lowercase) are stripped as "<scope>/{param}" pairs; literals
// are bare segments that carry no hierarchy.
var (
	scopeNames = map[string]bool{
		"projects": true, "organizations": true, "folders": true, "billingaccounts": true, "customers": true, "customer": true,
		"locations": true, "zones": true, "regions": true,
	}
	// zonesRegions strips only in the compute shape; see scopesFor.
	scopeNamesNoZone = func() map[string]bool {
		out := map[string]bool{}
		for k, v := range scopeNames {
			if k != "zones" && k != "regions" {
				out[k] = v
			}
		}
		return out
	}()
	literals = map[string]bool{"global": true, "aggregated": true}
	// knativeRoot is run v1's project alias: "namespaces/{namespace}/services".
	// Only a leading pair is a scope; "namespaces" under a real root (iam
	// workload identity pools) is a resource collection.
	knativeRoot = "namespaces"
	versionRe   = regexp.MustCompile(`^v\d`)
	paramRe     = regexp.MustCompile(`\[\^/\]\+`)
)

type entry struct {
	api      string
	docPath  []string // resource node names from the document tree, scope nodes removed
	depth    int
	parents  []string
	parent   string
	class    sdkinv.Class
	rule     string // the classification rule that decided class
	signals  map[string]bool
	ops      map[string]sdkinv.Operation // by label+module
	versions map[string]bool
	refs     map[string]bool
}

func (e extractor) Extract(_ context.Context, dir string) (*sdkinv.Universe, error) {
	root := filepath.Join(dir, "api")
	u := &sdkinv.Universe{Provider: "gcp", Pins: map[string]string{modulePath: e.Ref()}}
	entries := map[string]*entry{}
	excluded := map[string]bool{}
	identities := map[string][]string{} // service identity -> document names
	var skipped []string                // "<api>/<version>" documents with no cloud-rooted lister
	var uncloud []sdkinv.Drop           // their ops, reason decided once every version is read
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, "-api.json") {
			return err
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		var dc doc
		if jerr := json.Unmarshal(raw, &dc); jerr != nil {
			u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{Severity: "warn", Source: p, Message: jerr.Error()})
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if dc.RootURL != "" {
			identities[dc.identity()] = append(identities[dc.identity()], dc.Name)
		}
		docDir := filepath.ToSlash(filepath.Dir(rel))
		u.SourceOps = append(u.SourceOps, sourceOps(&dc, docDir, e.Ref())...)
		cloud, others, drops := indexDoc(entries, &dc, docDir, e.Ref())
		if !cloud {
			excluded[dc.Name] = true
			skipped = append(skipped, dc.Name+"/"+dc.Version)
		}
		for _, d := range drops {
			if d.Reason == "" {
				uncloud = append(uncloud, d)
			} else {
				u.Dropped = append(u.Dropped, d)
			}
		}
		u.Other = append(u.Other, others...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	aliases, aliasOps := dropAliasDocs(entries, identities)
	for _, o := range aliasOps {
		u.Dropped = append(u.Dropped, sdkinv.Drop{Op: o, Reason: "alias-document"})
	}
	if len(aliases) > 0 {
		u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{
			Severity: "info", Source: "discovery",
			Message: fmt.Sprintf("alias documents dropped for duplicating another API's service: %s", strings.Join(aliases, " ")),
		})
		kept := u.Other[:0]
		for _, o := range u.Other {
			if slices.Contains(aliases, o.Service) {
				u.Dropped = append(u.Dropped, sdkinv.Drop{Op: o, Reason: "alias-document"})
			} else {
				kept = append(kept, o)
			}
		}
		u.Other = kept
	}
	// An API rooted in a cloud container in one version is in the universe in
	// every version, so the exclusion is dropped for those — but this version's
	// own listers are not indexed, and neither are its other ops.
	for _, en := range entries {
		delete(excluded, en.api)
	}
	u.Dropped = append(u.Dropped, uncloudDrops(uncloud, excluded, aliases)...)
	var dropped []string
	for _, av := range skipped {
		if api, _, _ := strings.Cut(av, "/"); !excluded[api] {
			dropped = append(dropped, av)
		}
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{
			Severity: "info", Source: "discovery",
			Message: fmt.Sprintf("%d document versions of universe APIs have no cloud-rooted lister and are not indexed: %s", len(dropped), strings.Join(dropped, " ")),
		})
	}
	// Parent must name a candidate key (README, conformance). The document
	// tree nests nodes that are not listable themselves (appengine/apps), so
	// walk up to the nearest ancestor that is a candidate, and clear it when
	// none is; Depth stays the template's parent-id count either way.
	for _, en := range entries {
		for en.parent != "" && entries[en.parent] == nil {
			cut := strings.LastIndex(en.parent, "/")
			if cut < 0 || !strings.Contains(en.parent[:cut], "/") {
				en.parent = ""
				break
			}
			en.parent = en.parent[:cut]
		}
	}
	if len(excluded) > 0 {
		names := make([]string, 0, len(excluded))
		for n := range excluded {
			names = append(names, n)
		}
		sort.Strings(names)
		u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{Severity: "info", Source: "discovery", Message: fmt.Sprintf("%d non-cloud APIs excluded: %s", len(names), strings.Join(names, " "))})
	}
	for key, en := range entries {
		u.Candidates = append(u.Candidates, toCandidate(key, en))
	}
	sdkinv.SortCandidates(u.Candidates)
	// Non-cloud APIs are outside the universe entirely, other ops included.
	kept := u.Other[:0]
	for _, o := range u.Other {
		if excluded[o.Service] {
			u.Dropped = append(u.Dropped, sdkinv.Drop{Op: o, Reason: "non-cloud-api"})
		} else {
			kept = append(kept, o)
		}
	}
	u.Other = kept
	sdkinv.SortOps(u.Other)
	sdkinv.SortOpRefs(u.SourceOps)
	sdkinv.SortDrops(u.Dropped)
	u.Scopes = slices.Clone(scopeVocab)
	return u, nil
}

// dropAliasDocs removes the entries of every document name that shares a
// service identity with another: the name matching the rootUrl's own host
// label wins, else the alphabetically first. Returns the names dropped and the
// candidate ops that went with them.
func dropAliasDocs(entries map[string]*entry, identities map[string][]string) ([]string, []sdkinv.Operation) {
	dropped := map[string]bool{}
	for id, names := range identities {
		uniq := map[string]bool{}
		for _, n := range names {
			uniq[n] = true
		}
		if len(uniq) < 2 {
			continue
		}
		sorted := make([]string, 0, len(uniq))
		for n := range uniq {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		host, _, _ := strings.Cut(strings.TrimPrefix(strings.SplitN(id, "\x00", 2)[0], "https://"), ".")
		keep := sorted[0]
		if slices.Contains(sorted, host) {
			keep = host
		}
		for _, n := range sorted {
			if n != keep {
				dropped[n] = true
			}
		}
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	var ops []sdkinv.Operation
	for k, en := range entries {
		if dropped[en.api] {
			for _, o := range en.ops {
				ops = append(ops, o)
			}
			delete(entries, k)
		}
	}
	out := make([]string, 0, len(dropped))
	for n := range dropped {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, ops
}

type lister struct {
	docPath []string
	name    string // "list" | "aggregatedList"
	m       *method
	node    *resource
}

// indexDoc files every list method of one document version. Returns false
// when no lister is rooted in a cloud container (API excluded).
// collectListers walks a document's resource tree and splits its methods into
// the listers that can key a candidate and everything else, which ships as
// Universe.Other for pairing.
func collectListers(dc *doc, docDir, ref string) ([]lister, []sdkinv.Operation) {
	var listers []lister
	var others []sdkinv.Operation
	var walk func(res map[string]*resource, path []string)
	walk = func(res map[string]*resource, path []string) {
		for _, n := range slices.Sorted(maps.Keys(res)) {
			r := res[n]
			p := append(append([]string{}, path...), n)
			_, hasList := r.Methods["list"]
			// Sorted: map order decided which lister of a node was indexed
			// first, and with #47 that decided which element the node was
			// recorded as enumerating — two extractions disagreed.
			for _, mn := range slices.Sorted(maps.Keys(r.Methods)) {
				m := r.Methods[mn]
				// search/fetch/listPolicies enumerate a collection the node
				// has no list for (iam/policies, cloudresourcemanager
				// organizations in v1 and v3). A node that also has a list is
				// not admitted twice: the search is then a filtered view.
				if mn == "list" || mn == "aggregatedList" || (!hasList && altListers[mn]) {
					listers = append(listers, lister{docPath: p, name: mn, m: m, node: r})
					continue
				}
				others = append(others, methodOp(dc, p, mn, m, docDir, ref))
			}
			walk(r.Resources, p)
		}
	}
	walk(dc.Resources, nil)
	return listers, others
}

// indexDoc also returns the ops it leaves out. When no lister is cloud-rooted
// that is every op of the document, with an empty reason the caller fills in
// once it knows whether another version of the API is in the universe.
func indexDoc(entries map[string]*entry, dc *doc, docDir, ref string) (bool, []sdkinv.Operation, []sdkinv.Drop) {
	listers, others := collectListers(dc, docDir, ref)
	drops := rootDrops(dc, docDir, ref)

	cloud := false
	for _, l := range listers {
		if cloudRooted(template(l.m, dc.Version), l.m) {
			cloud = true
			break
		}
	}
	if !cloud {
		for _, l := range listers {
			others = append(others, methodOp(dc, l.docPath, l.name, l.m, docDir, ref))
		}
		for _, o := range others {
			drops = append(drops, sdkinv.Drop{Op: o})
		}
		return false, nil, drops
	}
	preview := strings.Contains(dc.Version, "alpha") || strings.Contains(dc.Version, "beta")
	schemas := &schemaSet{raw: dc.Schemas}
	// elementOf and aggregated support #47: an aggregatedList enumerates every
	// scope's collection, so it is also an op of the regional twin. disco
	// lists the regional Compute types exclusively through the base call, and
	// nine rows rested on the name-match fallback for it.
	elementOf := map[string]string{}
	var aggregated []aggLister
	for _, l := range listers {
		tmpl := template(l.m, dc.Version)
		segs := dropGroupingRoot(dropKnativeRoot(sdkinv.ParseTemplate(tmpl)))
		knative := len(segs) < len(sdkinv.ParseTemplate(tmpl))
		scopes := scopesFor(segs)
		rp := sdkinv.StripScopes(segs, scopes, literals)
		if rp.Item || len(rp.Statics) == 0 {
			reason := "lister-on-item-path"
			if !rp.Item {
				reason = "lister-without-collection-segment"
			}
			drops = append(drops, sdkinv.Drop{Op: methodOp(dc, l.docPath, l.name, l.m, docDir, ref), Reason: reason})
			continue
		}
		docPath := stripScopeNodes(dropGroupingNode(dropKnativeNode(l.docPath), tmpl), scopes)
		// Lower-cased so one collection reached through several versions
		// (run v1 "workerpools", v2 "workerPools") is one candidate.
		key := dc.Name + "/" + strings.ToLower(strings.Join(docPath, "/"))
		en := entries[key]
		if en == nil {
			en = &entry{api: dc.Name, docPath: docPath, signals: map[string]bool{}, ops: map[string]sdkinv.Operation{}, versions: map[string]bool{}, refs: map[string]bool{}}
			entries[key] = en
		}
		en.versions[dc.Version] = true
		if !preview {
			en.signals["stable"] = true
		}
		scope := scopeOf(segs, tmpl, knative, l.m)
		en.signals["scope:"+string(scope)] = true
		if len(rp.Parents) > en.depth || en.parent == "" {
			en.depth = len(rp.Parents)
			en.parents = rp.Parents
			en.parent = parentKey(dc, docPath, rp.Parents, segs)
		}
		class, rule := classify(l.node, en.signals)
		if stronger := sdkinv.StrongerClass(en.class, class); en.class == "" || stronger != en.class {
			en.class, en.rule = stronger, rule
		} else if class == en.class && (en.rule == "" || rule < en.rule) {
			en.rule = rule
		}
		// A lister at the document root has no resource node, so there is no
		// collection noun to prefer an element by.
		noun := ""
		if len(docPath) > 0 {
			noun = docPath[len(docPath)-1]
		}
		for _, r := range schemas.refsOf(l.m.Response.Ref, noun) {
			en.refs[r] = true
		}
		var required, targets []string
		for i, sg := range segs {
			if sg.Param && i > 0 && !segs[i-1].Param && !scopeNames[strings.ToLower(segs[i-1].Text)] {
				required = append(required, sg.Text)
			}
		}
		targets = append(targets, rp.Parents...)
		label := dc.Name + ":" + strings.Join(l.docPath, ".") + "." + l.name
		module := fmt.Sprintf("%s@%s/%s", modulePath, ref, docDir)
		op := sdkinv.Operation{
			Service: dc.Name, Name: strings.Join(l.docPath, ".") + "." + l.name, Label: label,
			IsList: true, Paged: hasParam(l.m, "pageToken"), Required: required, Targets: targets,
			Scope: scope, Path: tmpl, Module: module,
		}
		en.ops[label+"@"+module] = op
		if el := schemas.element(l.m.Response.Ref, noun, map[string]bool{}); el != "" {
			if _, seen := elementOf[key]; !seen {
				elementOf[key] = el
			}
			if l.name == "aggregatedList" {
				aggregated = append(aggregated, aggLister{key: key, element: el, opKey: label + "@" + module, op: op, leaf: noun})
			}
		}
	}
	attachAggregated(entries, elementOf, aggregated)
	return true, others, drops
}

// rootDrops records the methods declared at the document root, which the
// resource walk never reaches.
func rootDrops(dc *doc, docDir, ref string) []sdkinv.Drop {
	var out []sdkinv.Drop
	for _, mn := range slices.Sorted(maps.Keys(dc.Methods)) {
		out = append(out, sdkinv.Drop{Op: methodOp(dc, nil, mn, dc.Methods[mn], docDir, ref), Reason: "document-root-method"})
	}
	return out
}

// uncloudDrops names why a document with no cloud-rooted lister was left out:
// its API is an alias of another, the whole API is outside the universe, or
// only this version is.
func uncloudDrops(ds []sdkinv.Drop, excluded map[string]bool, aliases []string) []sdkinv.Drop {
	for i := range ds {
		switch svc := ds[i].Op.Service; {
		case slices.Contains(aliases, svc):
			ds[i].Reason = "alias-document"
		case excluded[svc]:
			ds[i].Reason = "non-cloud-api"
		default:
			ds[i].Reason = "version-without-cloud-rooted-lister"
		}
	}
	return ds
}

// methodOp is the Operation for one Discovery method at path p ("" at the
// document root).
func methodOp(dc *doc, p []string, mn string, m *method, docDir, ref string) sdkinv.Operation {
	name := strings.Join(append(slices.Clone(p), mn), ".")
	return sdkinv.Operation{
		Service: dc.Name, Name: name, Label: dc.Name + ":" + name,
		Path: template(m, dc.Version), Module: fmt.Sprintf("%s@%s/%s", modulePath, ref, docDir),
	}
}

// sourceOps enumerates every method a document declares, root included,
// independent of classification.
func sourceOps(dc *doc, docDir, ref string) []sdkinv.OpRef {
	var out []sdkinv.OpRef
	var walk func(methods map[string]*method, res map[string]*resource, path []string)
	walk = func(methods map[string]*method, res map[string]*resource, path []string) {
		for mn, m := range methods {
			out = append(out, methodOp(dc, path, mn, m, docDir, ref).Ref())
		}
		for n, r := range res {
			walk(r.Methods, r.Resources, append(slices.Clone(path), n))
		}
	}
	walk(dc.Methods, dc.Resources, nil)
	return out
}

// aggLister is one aggregatedList call and the element it enumerates.
type aggLister struct {
	key, element, opKey, leaf string
	op                        sdkinv.Operation
}

// attachAggregated registers an aggregatedList on every sibling collection of
// the same document that enumerates the same element: the response is a map of
// scoped lists, so the regional twin is listed by that one call.
func attachAggregated(entries map[string]*entry, elementOf map[string]string, aggregated []aggLister) {
	for _, a := range aggregated {
		for _, key := range slices.Sorted(maps.Keys(elementOf)) {
			if key == a.key || elementOf[key] != a.element {
				continue
			}
			en := entries[key]
			if en == nil {
				continue
			}
			if _, dup := en.ops[a.opKey]; dup {
				continue
			}
			en.ops[a.opKey] = a.op
			en.signals["aggregated-by:"+a.leaf] = true
		}
	}
}

// template returns the concrete path template: flatPath, else path with
// {+param} placeholders expanded from the parameter's pattern.
func template(m *method, version string) string {
	t := m.FlatPath
	if t == "" {
		t = m.Path
		for name, p := range m.Parameters {
			ph := "{+" + name + "}"
			if !strings.Contains(t, ph) {
				continue
			}
			t = strings.Replace(t, ph, expandPattern(p.Pattern, name), 1)
		}
	}
	// Drop the leading service/version prefix ("v1/", "admin/directory/v1/").
	parts := strings.Split(strings.Trim(t, "/"), "/")
	for i, p := range parts {
		// The document's own version too: Deployment Manager's alpha document
		// spells it "alpha", so "^v\d" stripped nothing and the whole API
		// dropped out of the universe for not being cloud-rooted.
		if versionRe.MatchString(p) || p == version {
			parts = parts[i+1:]
			break
		}
	}
	return strings.Join(parts, "/")
}

// expandPattern turns "^projects/[^/]+/locations/[^/]+$" into
// "projects/{p}/locations/{p}"; a generic "^[^/]+/[^/]+$" (any container)
// becomes "{p}/{p}".
func expandPattern(pattern, name string) string {
	p := strings.TrimSuffix(strings.TrimPrefix(pattern, "^"), "$")
	if p == "" {
		return "{" + name + "}"
	}
	if strings.HasPrefix(p, "(") { // "(projects|folders|organizations)/…" — take the first alternative
		if i := strings.Index(p, ")"); i > 0 {
			alts := strings.Split(strings.Trim(p[:i+1], "()"), "|")
			p = alts[0] + p[i+1:]
		}
	}
	return paramRe.ReplaceAllString(p, "{"+name+"}")
}

// scopesFor narrows "zones"/"regions" to the compute shape
// "projects/{p}/(zones|regions)/{x}". Dataplex nests assets under
// "lakes/{lake}/zones/{zone}" where the zone is itself a create-capable
// collection, and stripping it merged two collections into one key.
func scopesFor(segs []sdkinv.Segment) map[string]bool {
	for i := 0; i+1 < len(segs); i++ {
		l := strings.ToLower(segs[i].Text)
		if segs[i].Param || (l != "zones" && l != "regions") || !segs[i+1].Param {
			continue
		}
		if i >= 2 && !segs[i-2].Param && strings.EqualFold(segs[i-2].Text, "projects") && segs[i-1].Param {
			continue
		}
		return scopeNamesNoZone
	}
	return scopeNames
}

// cloudRooted decides whether a lister enumerates within a Google Cloud
// tenancy container. Reading only the first segment dropped four real APIs
// wholesale: Cloud Asset and Service Usage expand {+parent} to a generic
// "{id}/{id}" because the parameter accepts any of projects, folders or
// organizations -- which its own description spells out -- and Pub/Sub Lite
// puts a static "admin" in front of the project.
func cloudRooted(tmpl string, m *method) bool {
	segs := sdkinv.ParseTemplate(tmpl)
	if len(segs) == 0 {
		return false
	}
	if _, ok := cloudRoots[rootOf(tmpl)]; ok {
		return true
	}
	if segs[0].Param {
		return genericContainerParam(m)
	}
	// A leading static that only groups a cloud root behind it is the API's
	// own prefix: Pub/Sub Lite spells its project paths "admin/projects/{p}".
	// Deeper roots are deliberately not accepted — a cloud root anywhere in
	// the path admits DFA reporting, Tag Manager and the Cloud Channel
	// reseller API, which are not cloud infrastructure.
	if len(segs) > 1 && !segs[1].Param {
		_, ok := cloudRoots[strings.ToLower(segs[1].Text)]
		return ok
	}
	return false
}

// genericContainerParam reports whether a path parameter accepts a cloud
// container. The pattern cannot say so (it is "^[^/]+/[^/]+$"); the
// description names the formats it takes.
func genericContainerParam(m *method) bool {
	for _, p := range m.Parameters {
		if p.Location != "path" {
			continue
		}
		for root := range cloudRoots {
			if strings.Contains(strings.ToLower(p.Description), root+"/") {
				return true
			}
		}
	}
	return false
}

func rootOf(tmpl string) string {
	seg := strings.SplitN(tmpl, "/", 2)[0]
	return strings.ToLower(seg)
}

// scopeOf reads the scope from the template root. knative says the caller
// already stripped run v1's "namespaces/{namespace}" project alias, and a
// required project/parent query parameter carries the container for listers
// whose path does not (storage buckets) — both read as global otherwise, which
// contradicted the candidate's own scope:project signal on 48 rows.
func scopeOf(segs []sdkinv.Segment, tmpl string, knative bool, m *method) sdkinv.Scope {
	if len(segs) > 0 && segs[0].Param {
		return ScopeProject // generic "{parent}" accepting any container
	}
	if sc, ok := cloudRoots[rootOf(tmpl)]; ok {
		return sc
	}
	if knative || requiredProjectQuery(m) {
		return ScopeProject
	}
	return ScopeGlobal
}

// requiredProjectQuery reports a required query parameter naming the project
// or the parent container.
func requiredProjectQuery(m *method) bool {
	for name, p := range m.Parameters {
		if p.Required && p.Location == "query" && (name == "project" || name == "parent" || name == "projectId") {
			return true
		}
	}
	return false
}

func dropKnativeRoot(segs []sdkinv.Segment) []sdkinv.Segment {
	if len(segs) > 2 && !segs[0].Param && strings.EqualFold(segs[0].Text, knativeRoot) && segs[1].Param {
		return segs[2:]
	}
	return segs
}

// dropGroupingRoot removes a leading static that only groups a cloud root
// behind it: Pub/Sub Lite's "admin/projects/{p}/..." and "cursor/projects/...".
// Keeping it put the grouping word in the key, where no ARM-style or disco
// name could ever match it.
func dropGroupingRoot(segs []sdkinv.Segment) []sdkinv.Segment {
	if len(segs) > 1 && !segs[0].Param && !segs[1].Param {
		if _, ok := cloudRoots[strings.ToLower(segs[1].Text)]; ok {
			if _, isRoot := cloudRoots[strings.ToLower(segs[0].Text)]; !isRoot {
				return segs[1:]
			}
		}
	}
	return segs
}

// dropGroupingNode removes the matching document node for dropGroupingRoot.
func dropGroupingNode(docPath []string, tmpl string) []string {
	segs := sdkinv.ParseTemplate(tmpl)
	if len(docPath) > 1 && len(segs) > 1 && !segs[0].Param && strings.EqualFold(docPath[0], segs[0].Text) {
		if _, ok := cloudRoots[strings.ToLower(segs[1].Text)]; ok {
			return docPath[1:]
		}
	}
	return docPath
}

func dropKnativeNode(docPath []string) []string {
	if len(docPath) > 1 && strings.EqualFold(docPath[0], knativeRoot) {
		return docPath[1:]
	}
	return docPath
}

// stripScopeNodes drops container nodes from a document path, keeping the
// last node even when it is itself a container collection ("projects",
// "zones" are real listable collections at the leaf).
func stripScopeNodes(docPath []string, scopes map[string]bool) []string {
	out := make([]string, 0, len(docPath))
	for i, n := range docPath {
		if i < len(docPath)-1 && (scopes[strings.ToLower(n)] || literals[strings.ToLower(n)]) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// parentKey names the parent candidate: the enclosing document node when the
// tree is nested, else the top-level collection whose singular matches the
// parent segment or its parameter (storage "b/{bucket}" → "buckets").
func parentKey(dc *doc, docPath, parents []string, segs []sdkinv.Segment) string {
	if len(parents) == 0 {
		return ""
	}
	if len(docPath) > 1 {
		return dc.Name + "/" + strings.ToLower(strings.Join(docPath[:len(docPath)-1], "/"))
	}
	last := parents[len(parents)-1]
	paramName := ""
	for i, s := range segs {
		if !s.Param && s.Text == last && i+1 < len(segs) && segs[i+1].Param {
			paramName = segs[i+1].Text
		}
	}
	for n := range dc.Resources {
		cs := sdkinv.CanonSingular(n)
		if cs == sdkinv.CanonSingular(last) || (paramName != "" && cs == sdkinv.CanonSingular(paramName)) {
			return dc.Name + "/" + strings.ToLower(n) // keys are lower-cased; Discovery spells nodes camelCase
		}
	}
	return dc.Name + "/" + strings.ToLower(last)
}

// classify reads the sibling methods on the resource node: nodes whose get
// returns a long-running Operation are job records, not resources; creatable
// nodes are resources; delete-only nodes are records the caller cannot
// create; get-only nodes are catalogs.
func classify(node *resource, signals map[string]bool) (sdkinv.Class, string) {
	has := func(names ...string) bool {
		for _, n := range names {
			if _, ok := node.Methods[n]; ok {
				return true
			}
		}
		return false
	}
	switch {
	case isOperationNode(node):
		signals["operation"] = true
		return sdkinv.ClassNonResource, "operation-node"
	case has("insert", "create"):
		signals["create"] = true
		return sdkinv.ClassResource, "create"
	case has("delete"):
		signals["delete-only"] = true
		return sdkinv.ClassResource, "delete-only"
	case has("patch", "update", "destroy", "undelete"):
		// README defines catalog as provider-published and read-only, and a
		// node the caller can patch or destroy is neither. setIamPolicy alone
		// is too weak — it would pull in four genuine catalogs.
		signals["mutable"] = true
		return sdkinv.ClassResource, "mutable"
	case has("get"):
		signals["get-only"] = true
		return sdkinv.ClassCatalog, "get-only"
	default:
		signals["list-only"] = true
		return sdkinv.ClassNonResource, "list-only"
	}
}

// isOperationNode: google.longrunning.Operation (and compute's Operation)
// always surfaces as a schema named "…Operation"; a node whose get returns it
// enumerates operation records.
func isOperationNode(node *resource) bool {
	if g, ok := node.Methods["get"]; ok && strings.HasSuffix(g.Response.Ref, "Operation") {
		return true
	}
	if l, ok := node.Methods["list"]; ok && strings.HasSuffix(l.Response.Ref, "ListOperationsResponse") {
		return true
	}
	return false
}

func hasParam(m *method, name string) bool {
	_, ok := m.Parameters[name]
	return ok
}

func toCandidate(key string, en *entry) sdkinv.Candidate {
	c := sdkinv.Candidate{Provider: "gcp", Service: en.api, Key: key, Depth: en.depth, Class: en.class, Rule: en.rule}
	if en.depth > 0 {
		c.Parent = en.parent
	}
	if !en.signals["stable"] {
		en.signals["preview-only"] = true
	}
	delete(en.signals, "stable")
	for s := range en.signals {
		c.Signals = append(c.Signals, s)
	}
	sort.Strings(c.Signals)
	for _, op := range en.ops {
		c.Ops = append(c.Ops, op)
	}
	for r := range en.refs {
		c.Refs = append(c.Refs, r)
	}
	sort.Strings(c.Refs)
	return c
}
