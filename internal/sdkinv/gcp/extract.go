package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// Discovery document subset. schemas are deliberately not decoded.
type doc struct {
	Name      string               `json:"name"`
	Version   string               `json:"version"`
	Resources map[string]*resource `json:"resources"`
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
	Required bool   `json:"required"`
	Location string `json:"location"`
	Pattern  string `json:"pattern"`
}

// cloudRoots are the tenancy containers Google Cloud / Workspace list within.
// An API belongs to the universe only when at least one lister is rooted in
// one of them; APIs rooted elsewhere (youtube channels, adsense accounts) are
// not cloud infrastructure.
var cloudRoots = map[string]sdkinv.Scope{
	"projects": sdkinv.ScopeProject, "organizations": sdkinv.ScopeOrg, "folders": sdkinv.ScopeFolder,
	"billingaccounts": sdkinv.ScopeBillingAccount, "customers": sdkinv.ScopeTenant, "customer": sdkinv.ScopeTenant,
}

// scopeNames (lowercase) are stripped as "<scope>/{param}" pairs; literals
// are bare segments that carry no hierarchy.
var (
	scopeNames = map[string]bool{
		"projects": true, "organizations": true, "folders": true, "billingaccounts": true, "customers": true, "customer": true,
		"locations": true, "zones": true, "regions": true,
	}
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
	signals  map[string]bool
	ops      map[string]sdkinv.Operation // by label+module
	versions map[string]bool
}

func (e extractor) Extract(_ context.Context, dir string) (*sdkinv.Universe, error) {
	root := filepath.Join(dir, "api")
	u := &sdkinv.Universe{Provider: "gcp", Pins: map[string]string{modulePath: e.Ref()}}
	entries := map[string]*entry{}
	excluded := map[string]bool{}
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
		cloud, others := indexDoc(entries, &dc, filepath.ToSlash(filepath.Dir(rel)), e.Ref())
		if !cloud {
			excluded[dc.Name] = true
		}
		u.Other = append(u.Other, others...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// An API rooted in a cloud container in one version is in the universe in
	// every version; drop the exclusion for those.
	for _, en := range entries {
		delete(excluded, en.api)
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
		if !excluded[o.Service] {
			kept = append(kept, o)
		}
	}
	u.Other = kept
	sdkinv.SortOps(u.Other)
	return u, nil
}

type lister struct {
	docPath []string
	name    string // "list" | "aggregatedList"
	m       *method
	node    *resource
}

// indexDoc files every list method of one document version. Returns false
// when no lister is rooted in a cloud container (API excluded).
func indexDoc(entries map[string]*entry, dc *doc, docDir, ref string) (bool, []sdkinv.Operation) {
	var listers []lister
	var others []sdkinv.Operation
	var walk func(res map[string]*resource, path []string)
	walk = func(res map[string]*resource, path []string) {
		names := make([]string, 0, len(res))
		for n := range res {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			r := res[n]
			p := append(append([]string{}, path...), n)
			for mn, m := range r.Methods {
				if mn == "list" || mn == "aggregatedList" {
					listers = append(listers, lister{docPath: p, name: mn, m: m, node: r})
					continue
				}
				others = append(others, sdkinv.Operation{
					Service: dc.Name, Name: strings.Join(p, ".") + "." + mn, Label: dc.Name + ":" + strings.Join(p, ".") + "." + mn,
					Path: template(m), Module: fmt.Sprintf("%s@%s/%s", modulePath, ref, docDir),
				})
			}
			walk(r.Resources, p)
		}
	}
	walk(dc.Resources, nil)

	cloud := false
	for _, l := range listers {
		if _, ok := cloudRoots[rootOf(template(l.m))]; ok {
			cloud = true
			break
		}
	}
	if !cloud {
		return false, nil
	}
	preview := strings.Contains(dc.Version, "alpha") || strings.Contains(dc.Version, "beta")
	for _, l := range listers {
		tmpl := template(l.m)
		segs := dropKnativeRoot(sdkinv.ParseTemplate(tmpl))
		rp := sdkinv.StripScopes(segs, scopeNames, literals)
		if rp.Item || len(rp.Statics) == 0 {
			continue
		}
		docPath := stripScopeNodes(dropKnativeNode(l.docPath))
		// Lower-cased so one collection reached through several versions
		// (run v1 "workerpools", v2 "workerPools") is one candidate.
		key := dc.Name + "/" + strings.ToLower(strings.Join(docPath, "/"))
		en := entries[key]
		if en == nil {
			en = &entry{api: dc.Name, docPath: docPath, signals: map[string]bool{}, ops: map[string]sdkinv.Operation{}, versions: map[string]bool{}}
			entries[key] = en
		}
		en.versions[dc.Version] = true
		if !preview {
			en.signals["stable"] = true
		}
		scope := scopeOf(segs, tmpl)
		en.signals["scope:"+string(scope)] = true
		if len(rp.Parents) > en.depth || en.parent == "" {
			en.depth = len(rp.Parents)
			en.parents = rp.Parents
			en.parent = parentKey(dc, docPath, rp.Parents, segs)
		}
		en.class = sdkinv.StrongerClass(en.class, classify(l.node, en.signals))
		var required, targets []string
		for i, sg := range segs {
			if sg.Param && i > 0 && !scopeNames[strings.ToLower(segs[i-1].Text)] {
				required = append(required, sg.Text)
			}
		}
		targets = append(targets, rp.Parents...)
		label := dc.Name + ":" + strings.Join(l.docPath, ".") + "." + l.name
		module := fmt.Sprintf("%s@%s/%s", modulePath, ref, docDir)
		en.ops[label+"@"+module] = sdkinv.Operation{
			Service: dc.Name, Name: strings.Join(l.docPath, ".") + "." + l.name, Label: label,
			IsList: true, Paged: hasParam(l.m, "pageToken"), Required: required, Targets: targets,
			Scope: scope, Path: tmpl, Module: module,
		}
	}
	return true, others
}

// template returns the concrete path template: flatPath, else path with
// {+param} placeholders expanded from the parameter's pattern.
func template(m *method) string {
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
		if versionRe.MatchString(p) {
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

func rootOf(tmpl string) string {
	seg := strings.SplitN(tmpl, "/", 2)[0]
	return strings.ToLower(seg)
}

func scopeOf(segs []sdkinv.Segment, tmpl string) sdkinv.Scope {
	if len(segs) > 0 && segs[0].Param {
		return sdkinv.ScopeProject // generic "{parent}" accepting any container
	}
	if sc, ok := cloudRoots[rootOf(tmpl)]; ok {
		return sc
	}
	return sdkinv.ScopeGlobal
}

func dropKnativeRoot(segs []sdkinv.Segment) []sdkinv.Segment {
	if len(segs) > 2 && !segs[0].Param && strings.EqualFold(segs[0].Text, knativeRoot) && segs[1].Param {
		return segs[2:]
	}
	return segs
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
func stripScopeNodes(docPath []string) []string {
	out := make([]string, 0, len(docPath))
	for i, n := range docPath {
		if i < len(docPath)-1 && (scopeNames[strings.ToLower(n)] || literals[strings.ToLower(n)]) {
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
			return dc.Name + "/" + n
		}
	}
	return dc.Name + "/" + strings.ToLower(last)
}

// classify reads the sibling methods on the resource node: nodes whose get
// returns a long-running Operation are job records, not resources; creatable
// nodes are resources; delete-only nodes are records the caller cannot
// create; get-only nodes are catalogs.
func classify(node *resource, signals map[string]bool) sdkinv.Class {
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
		return sdkinv.ClassNonResource
	case has("insert", "create"):
		signals["create"] = true
		return sdkinv.ClassResource
	case has("delete"):
		signals["delete-only"] = true
		return sdkinv.ClassResource
	case has("get"):
		signals["get-only"] = true
		return sdkinv.ClassCatalog
	default:
		signals["list-only"] = true
		return sdkinv.ClassNonResource
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
	c := sdkinv.Candidate{Provider: "gcp", Service: en.api, Key: key, Depth: en.depth, Class: en.class}
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
	return c
}
