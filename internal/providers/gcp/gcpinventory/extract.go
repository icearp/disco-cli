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
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// Discovery document subset. Schemas stay raw and decode one at a time
// (refs.go).
type doc struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	RootURL       string `json:"rootUrl"`
	ServicePath   string `json:"servicePath"`
	CanonicalName string `json:"canonicalName"`
	Auth          struct {
		OAuth2 struct {
			Scopes map[string]json.RawMessage `json:"scopes"`
		} `json:"oauth2"`
	} `json:"auth"`
	Methods   map[string]*method         `json:"methods"`
	Resources map[string]*resource       `json:"resources"`
	Schemas   map[string]json.RawMessage `json:"schemas"`
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
	Path           string           `json:"path"`
	FlatPath       string           `json:"flatPath"`
	HTTPMethod     string           `json:"httpMethod"`
	ParameterOrder []string         `json:"parameterOrder"`
	Parameters     map[string]param `json:"parameters"`
	Request        bodyRef          `json:"request"`
	Response       bodyRef          `json:"response"`
}

type bodyRef struct {
	Ref string `json:"$ref"`
}

type param struct {
	Required bool   `json:"required"`
	Location string `json:"location"`
	Pattern  string `json:"pattern"`
}

// cloudPlatformScope is the OAuth scope that authorizes the Google Cloud
// APIs. An API whose documents accept it is cloud infrastructure; youtube,
// adsense and DFA reporting do not.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// cloudRoots is the declared tenancy vocabulary: the containers a listing
// enumerates within, keyed by the lowercase path segment. It names the scope a
// listing reports, and which "<root>/{id}" pairs are containers rather than
// parents. Discovery has no structured tenancy marker, so this is the
// provider's declaration, like any provider's Scopes.
var cloudRoots = map[string]sdkinv.Scope{
	"projects": ScopeProject, "organizations": ScopeOrg, "folders": ScopeFolder,
	"billingaccounts": ScopeBillingAccount, "customers": ScopeTenant, "customer": ScopeTenant,
}

// placements are the declared location containers inside a tenancy root.
var placements = map[string]bool{"locations": true, "zones": true, "regions": true}

// scopeVocab is the Discovery scope vocabulary, narrowest first.
var scopeVocab = []sdkinv.Scope{
	ScopeProject, ScopeOrg, ScopeFolder, ScopeBillingAccount, ScopeTenant, ScopeGlobal, ScopeUnscoped,
}

var paramRe = regexp.MustCompile(`\[\^/\]\+`)

// docMethod is one Discovery method with its path template parsed: the
// service prefix stripped and a custom verb cut off.
type docMethod struct {
	path   []string // resource node names, outermost first
	name   string
	m      *method
	segs   []sdkinv.Segment
	prefix []string // static segments the prefix strip removed
	verb   string
	norm   string // segs with every param spelled "{}"
}

func (dm *docMethod) label(api string) string {
	return api + ":" + strings.Join(append(slices.Clone(dm.path), dm.name), ".")
}

// docIndex is one document version's methods, indexed by path template.
type docIndex struct {
	dc      *doc
	module  string
	methods []*docMethod
	byNorm  map[string][]*docMethod
	schemas *schemaSet
	handles map[string]bool // schemas a DELETE answers: the document's change records
}

func newDocIndex(dc *doc, module string) *docIndex {
	x := &docIndex{dc: dc, module: module, byNorm: map[string][]*docMethod{}, schemas: &schemaSet{raw: dc.Schemas}}
	add := func(path []string, name string, m *method) {
		dm := &docMethod{path: path, name: name, m: m}
		dm.segs, dm.prefix, dm.verb = template(dc, m)
		dm.norm = normPath(dm.segs)
		x.methods = append(x.methods, dm)
		x.byNorm[dm.norm] = append(x.byNorm[dm.norm], dm)
	}
	for _, mn := range slices.Sorted(maps.Keys(dc.Methods)) {
		add(nil, mn, dc.Methods[mn])
	}
	var walk func(res map[string]*resource, path []string)
	walk = func(res map[string]*resource, path []string) {
		for _, n := range slices.Sorted(maps.Keys(res)) {
			p := append(slices.Clone(path), n)
			for _, mn := range slices.Sorted(maps.Keys(res[n].Methods)) {
				add(p, mn, res[n].Methods[mn])
			}
			walk(res[n].Resources, p)
		}
	}
	walk(dc.Resources, nil)
	x.handles = map[string]bool{}
	for _, dm := range x.methods {
		if dm.m.HTTPMethod == "DELETE" && dm.m.Response.Ref != "" {
			x.handles[dm.m.Response.Ref] = true
		}
	}
	return x
}

func (x *docIndex) op(dm *docMethod) sdkinv.Operation {
	return sdkinv.Operation{
		Service: x.dc.Name, Name: strings.Join(append(slices.Clone(dm.path), dm.name), "."), Label: dm.label(x.dc.Name),
		Path: dm.pathText(), Module: x.module,
	}
}

// pathText renders the parsed template back, param names and verb kept.
func (dm *docMethod) pathText() string {
	parts := make([]string, len(dm.segs))
	for i, s := range dm.segs {
		parts[i] = s.Text
		if s.Param {
			parts[i] = "{" + s.Text + "}"
		}
	}
	t := strings.Join(parts, "/")
	if dm.verb != "" {
		t += ":" + dm.verb
	}
	return t
}

// creates is the POST or PUT on the collection path itself: the insert or
// create of that collection. A custom verb (":search", ":import") says nothing.
func (x *docIndex) creates(coll string) *docMethod {
	for _, dm := range x.byNorm[coll] {
		if dm.verb == "" && (dm.m.HTTPMethod == "POST" || dm.m.HTTPMethod == "PUT") {
			return dm
		}
	}
	return nil
}

// carries reports that a body is the schema or wraps it one level down
// (compute's ReservationSubBlocksGetResponse holds the ReservationSubBlock).
func (x *docIndex) carries(body, ref string) bool {
	if body == "" || ref == "" {
		return false
	}
	if body == ref {
		return true
	}
	s := x.schemas.get(body)
	return s != nil && slices.ContainsFunc(slices.Collect(maps.Values(s.Properties)), func(p *schema) bool { return p != nil && p.Ref == ref })
}

// itemGet is the GET on the collection's item path, if any.
func (x *docIndex) itemGet(coll string) *docMethod {
	for _, dm := range x.byNorm[coll+"/{}"] {
		if dm.verb == "" && dm.m.HTTPMethod == "GET" {
			return dm
		}
	}
	return nil
}

// template is a method's path relative to the API: servicePath + flatPath
// (else path with {+x} expanded from the parameter pattern), with the custom
// verb cut off and the leading prefix removed. The prefix is every static that
// another static follows (compute/v1/, admin/directory/v1/, Pub/Sub Lite's
// admin/, run v1's apis/serving.knative.dev/v1/) plus the document's own
// version before a param ("v2/{+parent}/cases").
func template(dc *doc, m *method) (segs []sdkinv.Segment, prefix []string, verb string) {
	t := m.FlatPath
	if t == "" {
		t = m.Path
		for _, name := range slices.Sorted(maps.Keys(m.Parameters)) {
			if ph := "{+" + name + "}"; strings.Contains(t, ph) {
				t = strings.Replace(t, ph, expandPattern(m.Parameters[name].Pattern, name), 1)
			}
		}
	}
	path, verb := sdkinv.SplitVerb(strings.TrimSuffix(dc.ServicePath, "/") + "/" + strings.TrimPrefix(t, "/"))
	segs = sdkinv.ParseTemplate(path)
	for len(segs) > 1 && !segs[0].Param && (!segs[1].Param || isVersion(segs[0].Text, dc.Version)) {
		prefix = append(prefix, segs[0].Text)
		segs = segs[1:]
	}
	return segs, prefix, verb
}

func isVersion(seg, version string) bool {
	return seg == version || strings.HasSuffix(version, "_"+seg)
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

func normPath(segs []sdkinv.Segment) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = s.Text
		if s.Param {
			parts[i] = "{}"
		}
	}
	return strings.Join(parts, "/")
}

// lister is one method admitted as enumerating a collection.
type lister struct {
	dm      *docMethod
	key     string
	depth   int
	parents []string // item paths of its non-scope parents, innermost first
	el      elem
	how     string // which structural fact confirmed it
	rp      sdkinv.ResourcePath
}

// shape splits a template into its scope pairs, literals and resource path. A
// "<name>/{id}" pair is a scope when name is a declared tenancy root or
// placement and this document cannot create it: apigee creates organizations
// and Dataplex creates lake zones, so there they are parents. A static that
// another static follows mid-path ("global", "aggregated") is a literal.
func (x *docIndex) shape(segs []sdkinv.Segment) (rp sdkinv.ResourcePath, scopes map[string]bool, parents []string) {
	scopes, literals := map[string]bool{}, map[string]bool{}
	for i := 0; i+1 < len(segs); i++ {
		s := segs[i]
		if s.Param {
			continue
		}
		lower := strings.ToLower(s.Text)
		declared := cloudRoots[lower] != "" || placements[lower]
		switch {
		case !segs[i+1].Param && declared:
			// "locations/global": the container's id pinned as a static.
			scopes[lower], scopes[strings.ToLower(segs[i+1].Text)] = true, true
			literals[lower], literals[strings.ToLower(segs[i+1].Text)] = true, true
			i++
		case !segs[i+1].Param:
			literals[lower] = true
		case i+2 < len(segs) && declared && x.creates(normPath(segs[:i+1])) == nil:
			scopes[lower] = true
		}
	}
	rp = sdkinv.StripScopes(segs, scopes, literals)
	for i := len(segs) - 2; i >= 0; i-- {
		if !segs[i].Param && segs[i+1].Param && i+2 < len(segs) && !scopes[strings.ToLower(segs[i].Text)] {
			parents = append(parents, normPath(segs[:i+2]))
		}
	}
	return rp, scopes, parents
}

// listerOf admits a GET on a collection path whose response carries an array
// of items, when a sibling confirms what the items are: the item path's GET
// answers the same schema, the collection's create takes it, or (an
// aggregated list, whose items sit in a per-scope map) some GET answers it.
// Filtered views (listUsable, searchFeatures) enumerate a schema nothing
// else in their node reads or writes, and stay in Other.
func (x *docIndex) listerOf(dm *docMethod) (lister, bool) {
	if dm.m.HTTPMethod != "GET" || len(dm.segs) == 0 || dm.segs[len(dm.segs)-1].Param || dm.m.Response.Ref == "" {
		return lister{}, false
	}
	noun := dm.segs[len(dm.segs)-1].Text
	if len(dm.path) > 0 {
		noun = dm.path[len(dm.path)-1]
	}
	el := x.schemas.element(dm.m.Response.Ref, noun, map[string]bool{})
	create := x.creates(dm.norm)
	if el.s == nil && create != nil && create.m.Request.Ref != "" && x.schemas.untypedArray(dm.m.Response.Ref) {
		// admin's Aliases lists items typed "any"; the create body names them.
		el = elem{ref: create.m.Request.Ref, s: x.schemas.get(create.m.Request.Ref)}
	}
	if el.s == nil {
		return lister{}, false
	}
	how := x.confirm(dm.norm, el, create)

	if how == "" {
		return lister{}, false
	}
	rp, scopes, parents := x.shape(dm.segs)
	docPath := dm.path
	if len(docPath) == 0 {
		docPath = []string{noun}
	}
	// A method on a node that lists below the node's own item
	// (instances.listVmExtensionStates on instances/{instance}/…) enumerates a
	// sub-collection, keyed by its own static.
	if leaf := lastStatic(dm.segs); !strings.EqualFold(leaf, docPath[len(docPath)-1]) && slices.ContainsFunc(dm.segs[:len(dm.segs)-1], func(s sdkinv.Segment) bool {
		return !s.Param && strings.EqualFold(s.Text, docPath[len(docPath)-1])
	}) {
		docPath = append(slices.Clone(docPath), leaf)
	}
	// The key is the resource path minus container nodes: scope pairs and
	// the service prefix. Other literal groupings (admin's "resources",
	// securitycenter's per-product settings) stay: they tell collections
	// with one leaf name apart.
	lead := 0 // leading nodes the prefix strip removed (Pub/Sub Lite's admin)
	for lead < len(docPath)-1 && slices.ContainsFunc(dm.prefix, func(p string) bool { return strings.EqualFold(p, docPath[lead]) }) {
		lead++
	}
	var keep []string
	for i, n := range docPath[lead:] {
		if lower := strings.ToLower(n); i == len(docPath)-lead-1 || !scopes[lower] {
			keep = append(keep, lower)
		}
	}
	return lister{
		dm: dm, key: x.dc.Name + "/" + strings.Join(keep, "/"), depth: len(rp.Parents),
		parents: parents, el: el, how: how, rp: rp,
	}, true
}

// confirm names the sibling that says what a list's items are, "" for none.
// The item GET decides when there is one, so a filtered view over another
// schema (searchFeatures on featurestores) never borrows its node's other
// methods. Without one, a write on the item path that carries the schema, a
// DELETE on it (integrations list and delete, nothing else), the create body,
// or for an aggregated list any GET answering the schema confirms.
func (x *docIndex) confirm(coll string, el elem, create *docMethod) string {
	if g := x.itemGet(coll); g != nil {
		if el.ref == "" || x.carries(g.m.Response.Ref, el.ref) || x.carries(el.ref, g.m.Response.Ref) {
			return "item-get"
		}
		return ""
	}
	del := false
	for _, dm := range x.byNorm[coll+"/{}"] {
		if dm.verb != "" {
			continue
		}
		if x.carries(dm.m.Request.Ref, el.ref) || x.carries(dm.m.Response.Ref, el.ref) {
			return "item-write"
		}
		del = del || dm.m.HTTPMethod == "DELETE"
	}
	switch {
	case del:
		return "item-delete"
	case create != nil && (x.carries(create.m.Request.Ref, el.ref) || x.carries(create.m.Response.Ref, el.ref)):
		return "create-body"
	case el.viaMap && x.answers(el.ref):
		return "aggregated"
	}
	return ""
}

// answers reports a GET whose response is the schema.
func (x *docIndex) answers(ref string) bool {
	return slices.ContainsFunc(x.methods, func(dm *docMethod) bool {
		return dm.m.HTTPMethod == "GET" && dm.m.Response.Ref == ref
	})
}

// classify reads what the document can do to the collection by HTTP method
// and path, never by method name: a POST/PUT on the collection creates; a
// DELETE on the item removes; a PATCH, PUT or POST on the item (or on an
// item action sub-path) edits, and so
// a write on another collection answering the item creates it by another
// route (secrets:addVersion, jobs:snapshot); a GET on the item alone is the
// provider's read-only catalog. A collection whose items are operation records
// is a feed. Custom verbs on the item (:approve, :markAccepted) are only a
// signal. An aggregated list is classified by the collection whose item GET
// answers its element.
func (x *docIndex) classify(l lister, signals map[string]bool) (sdkinv.Class, string) {
	coll := l.dm.norm
	if l.el.viaMap {
		coll = x.home(l.el.ref)
	}
	del, mutable, get := false, x.edits(coll), false
	for _, dm := range x.byNorm[coll+"/{}"] {
		switch {
		case dm.verb != "":
			if dm.m.HTTPMethod != "GET" {
				signals["custom-verb"] = true
			}
		case dm.m.HTTPMethod == "DELETE":
			del = true
		case dm.m.HTTPMethod == "GET":
			get = true
		default:
			mutable = true
		}
	}
	writers := x.writers(coll, l.el.ref)
	switch {
	case coll != "" && x.creates(coll) != nil:
		signals["create"] = true
		return sdkinv.ClassResource, "create"
	case coll != "" && x.createsByVerb(coll, l.el.ref):
		// osconfig patchJobs:execute, aiplatform evaluations:import.
		signals["create-verb"] = true
		return sdkinv.ClassResource, "create"
	case isOperation(l.el) || writers < 0 || writers >= 2:
		signals["operation"] = true
		return sdkinv.ClassNonResource, "operation-node"
	case writers == 1:
		signals["created-elsewhere"] = true
		return sdkinv.ClassResource, "created-elsewhere"
	case del:
		signals["delete-only"] = true
		return sdkinv.ClassResource, "delete-only"
	case mutable:
		// README defines catalog as provider-published and read-only, and a
		// node the caller can edit is neither.
		signals["mutable"] = true
		return sdkinv.ClassResource, "mutable"
	case get:
		signals["get-only"] = true
		return sdkinv.ClassCatalog, "get-only"
	default:
		signals["list-only"] = true
		return sdkinv.ClassNonResource, "list-only"
	}
}

// createsByVerb reports a custom-verb POST on the collection itself that
// answers the element: a create under another name.
func (x *docIndex) createsByVerb(coll, ref string) bool {
	return slices.ContainsFunc(x.byNorm[coll], func(dm *docMethod) bool {
		return dm.verb != "" && dm.m.HTTPMethod == "POST" && x.carries(dm.m.Response.Ref, ref)
	})
}

// edits reports an action on the item spelled as a sub-path (compute's
// instances/{i}/start, regionInstanceGroups/{g}/setNamedPorts) answered by
// the document's change handle: a schema some DELETE answers (Operation).
// Reads spelled as POSTs (listInstances, testIamPermissions) and setIamPolicy
// answer something else; a sub-path with an item path below it is a
// sub-collection, and one with a GET is a singleton (regions/{r}/snapshotSettings). Custom verbs stay a signal: locations/{l}:batchTranslateText
// answers an Operation too, and a location is still the provider's catalog.
func (x *docIndex) edits(coll string) bool {
	if coll == "" {
		return false
	}
	return slices.ContainsFunc(x.methods, func(dm *docMethod) bool {
		if dm.m.HTTPMethod == "GET" || dm.m.Response.Ref == "" || !x.handles[dm.m.Response.Ref] {
			return false
		}
		if dm.verb != "" {
			return false
		}
		rest, ok := strings.CutPrefix(dm.norm, coll+"/{}/")
		return ok && rest != "{}" && !strings.Contains(rest, "/") && len(x.byNorm[dm.norm+"/{}"]) == 0 &&
			!slices.ContainsFunc(x.byNorm[dm.norm], func(o *docMethod) bool { return o.m.HTTPMethod == "GET" })
	})
}

// home is the collection path whose item GET answers the schema, "" for none.
func (x *docIndex) home(ref string) string {
	for _, dm := range x.methods {
		if dm.m.HTTPMethod == "GET" && dm.verb == "" && dm.m.Response.Ref == ref && len(dm.segs) > 0 && dm.segs[len(dm.segs)-1].Param {
			return strings.TrimSuffix(dm.norm, "/{}")
		}
	}
	return ""
}

// isOperation: the element has google.longrunning.Operation's shape — done,
// then response or error.
func isOperation(e elem) bool {
	return e.s != nil && e.s.Properties["done"] != nil && (e.s.Properties["response"] != nil || e.s.Properties["error"] != nil)
}

// writers counts the other collections whose POSTs answer the element; -1
// when an edit or delete elsewhere answers it. An element a DELETE or PATCH
// answers is the handle of that change (compute's Operation, dns's
// managedZones.patch), and so is one that POSTs on two or more other
// collections answer; one POST is a create by another route.
func (x *docIndex) writers(coll, ref string) int {
	if ref == "" {
		return 0
	}
	leaf := coll[strings.LastIndex(coll, "/")+1:]
	ws := map[string]bool{}
	for _, dm := range x.methods {
		if dm.m.HTTPMethod == "GET" || dm.m.Response.Ref != ref {
			continue
		}
		w := lastStatic(dm.segs)
		if w == leaf {
			continue
		}
		if dm.m.HTTPMethod != "POST" {
			return -1
		}
		ws[w] = true
	}
	return len(ws)
}

func lastStatic(segs []sdkinv.Segment) string {
	for i := len(segs) - 1; i >= 0; i-- {
		if !segs[i].Param {
			return segs[i].Text
		}
	}
	return ""
}

// scopeOf: the template's tenancy root, else a required query parameter
// naming one (storage buckets take ?project=), else global when the path
// holds no container. A path opening on a generic container param
// (cloudasset "{+parent}/assets" takes any of projects, folders,
// organizations) is unscoped.
func scopeOf(dm *docMethod) sdkinv.Scope {
	if len(dm.segs) == 0 || dm.segs[0].Param {
		return ScopeUnscoped
	}
	if sc, ok := cloudRoots[strings.ToLower(dm.segs[0].Text)]; ok {
		return sc
	}
	for _, name := range slices.Sorted(maps.Keys(dm.m.Parameters)) {
		p := dm.m.Parameters[name]
		if !p.Required || p.Location != "query" {
			continue
		}
		stem := strings.TrimSuffix(strings.ToLower(name), "id")
		for _, root := range slices.Sorted(maps.Keys(cloudRoots)) {
			if sdkinv.Ident(root) == sdkinv.Ident(stem) {
				return cloudRoots[root]
			}
		}
	}
	return ScopeGlobal
}

type entry struct {
	api      string
	depth    int
	parents  []string
	parent   string
	class    sdkinv.Class
	rule     string
	signals  map[string]bool
	ops      map[string]sdkinv.Operation // by label+module
	versions map[string]bool
	refs     map[string]bool
	elements map[string]bool // element schema names
}

func (e extractor) Extract(_ context.Context, dir string) (*sdkinv.Universe, error) {
	root := filepath.Join(dir, "api")
	u := &sdkinv.Universe{Provider: "gcp", Pins: map[string]string{modulePath: e.Ref()}}
	entries := map[string]*entry{}
	cloud := map[string]bool{}               // API name -> some document accepts cloud-platform
	identities := map[string][]string{}      // service identity -> document names
	items := map[string]map[string]string{}  // API -> collection item path -> key
	var unconfirmed []string                 // list-shaped GETs no sibling confirms
	byAPI := map[string][]sdkinv.Operation{} // Other, by API, until membership is known
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
		if dc.RootURL != "" {
			identities[dc.identity()] = append(identities[dc.identity()], dc.Name)
		}
		if _, ok := dc.Auth.OAuth2.Scopes[cloudPlatformScope]; ok {
			cloud[dc.Name] = true
		}
		rel, _ := filepath.Rel(root, p)
		x := newDocIndex(&dc, fmt.Sprintf("%s@%s/%s", modulePath, e.Ref(), filepath.ToSlash(filepath.Dir(rel))))
		for _, dm := range x.methods {
			u.SourceOps = append(u.SourceOps, x.op(dm).Ref())
		}
		others, unconf := indexDoc(entries, items, x)
		byAPI[dc.Name] = append(byAPI[dc.Name], others...)
		unconfirmed = append(unconfirmed, unconf...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	aliases := dropAliasDocs(entries, identities, byAPI)
	u.Diagnostics = append(u.Diagnostics, namesDiag("info", "alias documents dropped for duplicating another API's service", aliases)...)
	for _, api := range aliases {
		for _, o := range byAPI[api] {
			u.Dropped = append(u.Dropped, sdkinv.Drop{Op: o, Reason: "alias-document"})
		}
		delete(byAPI, api)
	}
	var excluded []string
	for _, api := range slices.Sorted(maps.Keys(byAPI)) {
		if cloud[api] {
			u.Other = append(u.Other, byAPI[api]...)
			continue
		}
		excluded = append(excluded, api)
		for _, o := range byAPI[api] {
			u.Dropped = append(u.Dropped, sdkinv.Drop{Op: o, Reason: "non-cloud-api"})
		}
	}
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		if en := entries[k]; !cloud[en.api] {
			for _, op := range en.ops {
				u.Dropped = append(u.Dropped, sdkinv.Drop{Op: op, Reason: "non-cloud-api"})
			}
			delete(entries, k)
		}
	}
	u.Diagnostics = append(u.Diagnostics, namesDiag("info", "APIs excluded for not accepting the cloud-platform OAuth scope (non-cloud APIs excluded)", excluded)...)
	u.Diagnostics = append(u.Diagnostics, namesDiag("info", "list-shaped GETs left in Other: no item GET, create body or aggregated read confirms their items", filterAPIs(unconfirmed, cloud))...)
	u.Diagnostics = append(u.Diagnostics, foldAliasRoutes(entries, items)...)
	u.Diagnostics = append(u.Diagnostics, resolveParents(entries, items)...)
	var unscoped []string
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		c := toCandidate(k, entries[k])
		for _, op := range c.Ops {
			if op.Scope == ScopeUnscoped {
				unscoped = append(unscoped, op.Label)
			}
		}
		u.Candidates = append(u.Candidates, c)
	}
	u.Diagnostics = append(u.Diagnostics, namesDiag("warn", "listers open on a generic container parameter; scope unscoped", unscoped)...)
	sdkinv.SortCandidates(u.Candidates)
	sdkinv.SortOps(u.Other)
	sdkinv.SortOpRefs(u.SourceOps)
	sdkinv.SortDrops(u.Dropped)
	u.Scopes = slices.Clone(scopeVocab)
	return u, nil
}

// namesDiag renders one diagnostic naming every item, or none for none.
func namesDiag(severity, msg string, names []string) []sdkinv.Diagnostic {
	if len(names) == 0 {
		return nil
	}
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	return []sdkinv.Diagnostic{{Severity: severity, Source: "discovery", Message: fmt.Sprintf("%d %s: %s", len(names), msg, strings.Join(names, " "))}}
}

func filterAPIs(labels []string, cloud map[string]bool) []string {
	var out []string
	for _, l := range labels {
		if api, _, _ := strings.Cut(l, ":"); cloud[api] {
			out = append(out, l)
		}
	}
	return out
}

// indexDoc files one document version's listers into entries and returns
// every other method as Other, plus the list-shaped GETs nothing confirmed.
func indexDoc(entries map[string]*entry, items map[string]map[string]string, x *docIndex) ([]sdkinv.Operation, []string) {
	var listers []lister
	var others []sdkinv.Operation
	var unconfirmed []string
	for _, dm := range x.methods {
		if l, ok := x.listerOf(dm); ok {
			listers = append(listers, l)
			continue
		}
		others = append(others, x.op(dm))
		if dm.m.HTTPMethod == "GET" && hasParam(dm.m, "pageToken") {
			unconfirmed = append(unconfirmed, dm.label(x.dc.Name))
		}
	}
	foldSameCollection(listers)
	api := x.dc.Name
	if items[api] == nil {
		items[api] = map[string]string{}
	}
	preview := strings.Contains(x.dc.Version, "alpha") || strings.Contains(x.dc.Version, "beta")
	elementOf := map[string]string{}
	var aggregated []aggLister
	for _, l := range listers {
		if !l.el.viaMap {
			if k, taken := items[api][l.dm.norm+"/{}"]; !taken || l.key < k {
				items[api][l.dm.norm+"/{}"] = l.key
			}
		}
		en := entries[l.key]
		if en == nil {
			en = &entry{api: api, signals: map[string]bool{}, ops: map[string]sdkinv.Operation{}, versions: map[string]bool{}, refs: map[string]bool{}, elements: map[string]bool{}}
			entries[l.key] = en
		}
		en.versions[x.dc.Version] = true
		if !preview {
			en.signals["stable"] = true
		}
		scope := scopeOf(l.dm)
		en.signals["scope:"+string(scope)] = true
		en.signals["list:"+l.how] = true
		if l.depth > en.depth || en.parents == nil {
			en.depth, en.parents = l.depth, l.parents
		}
		class, rule := x.classify(l, en.signals)
		if stronger := sdkinv.StrongerClass(en.class, class); en.class == "" || stronger != en.class {
			en.class, en.rule = stronger, rule
		} else if class == en.class && (en.rule == "" || rule < en.rule) {
			en.rule = rule
		}
		for _, r := range x.schemas.refsOf(l.el) {
			en.refs[r] = true
		}
		op := x.op(l.dm)
		op.IsList, op.Paged, op.Scope = true, hasParam(l.dm.m, "pageToken"), scope
		op.Required, op.Targets = slices.Clone(l.dm.m.ParameterOrder), l.rp.Parents
		opKey := op.Label + "@" + op.Module
		en.ops[opKey] = op
		if l.el.ref != "" {
			en.elements[l.el.ref] = true
			if _, seen := elementOf[l.key]; !seen {
				elementOf[l.key] = l.el.ref
			}
			if l.dm.name == "aggregatedList" || l.el.viaMap {
				aggregated = append(aggregated, aggLister{key: l.key, element: l.el.ref, opKey: opKey, op: op, leaf: l.key[strings.LastIndex(l.key, "/")+1:]})
			}
		}
	}
	attachAggregated(entries, elementOf, aggregated)
	return others, unconfirmed
}

// foldSameCollection keys a lister under the top-level route of its
// collection when the document has one: the same element under the same leaf
// name at depth 0. run v1 lists services as namespaces.services and as
// projects.locations.services; discoveryengine reaches dataStores directly
// and through collections. A route through a parent the document lists is a
// different collection (identitytoolkit's tenant-level IdP configs), and two
// nested routes are never folded (admin's group and user aliases).
func foldSameCollection(listers []lister) {
	top := map[string]lister{}
	group := func(l lister) string {
		return l.el.ref + "\x00" + l.key[strings.LastIndex(l.key, "/")+1:]
	}
	for _, l := range listers {
		if l.el.ref == "" || l.depth > 0 {
			continue
		}
		if t, ok := top[group(l)]; !ok || l.key < t.key {
			top[group(l)] = l
		}
	}
	listed := map[string]bool{}
	for _, l := range listers {
		listed[l.dm.norm+"/{}"] = true
	}
	for i, l := range listers {
		if slices.ContainsFunc(l.parents, func(p string) bool { return listed[p] }) {
			continue
		}
		if t, ok := top[group(l)]; ok && l.el.ref != "" && l.depth > 0 {
			listers[i].key, listers[i].depth, listers[i].parents = t.key, 0, nil
		}
	}
}

// foldAliasRoutes merges a nested collection reached only through a container
// the API lists nowhere into the one other collection of the API with the same
// leaf and element. run v1 reaches jobs, executions and worker pools under
// namespaces/{project}, an alias of the project that v2 spells
// projects/{p}/locations/{l}; the two documents name the element Job and
// GoogleCloudRunV2Job, so foldSameCollection, which works inside one document,
// cannot see it. The elements must agree up to a version prefix
// (sameElement). A leaf shared by two or more other collections names distinct things
// and stays.
func foldAliasRoutes(entries map[string]*entry, items map[string]map[string]string) []sdkinv.Diagnostic {
	leaf := func(k string) string { return k[strings.LastIndex(k, "/")+1:] }
	orphan := func(en *entry) bool {
		return en.depth > 0 && !slices.ContainsFunc(en.parents, func(p string) bool { return items[en.api][p] != "" })
	}
	byLeaf := map[string][]string{}
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		byLeaf[entries[k].api+"/"+leaf(k)] = append(byLeaf[entries[k].api+"/"+leaf(k)], k)
	}
	var folded []string
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		en := entries[k]
		if !orphan(en) {
			continue
		}
		others := slices.DeleteFunc(slices.Clone(byLeaf[en.api+"/"+leaf(k)]), func(o string) bool { return o == k })
		if len(others) != 1 || orphan(entries[others[0]]) || !sameElement(en.elements, entries[others[0]].elements) {
			continue
		}
		into := entries[others[0]]
		maps.Copy(into.ops, en.ops)
		maps.Copy(into.versions, en.versions)
		maps.Copy(into.refs, en.refs)
		for sg := range en.signals {
			into.signals[sg] = true
		}
		if stronger := sdkinv.StrongerClass(into.class, en.class); stronger != into.class {
			into.class, into.rule = stronger, en.rule
		}
		for p, v := range items[en.api] {
			if v == k {
				items[en.api][p] = others[0]
			}
		}
		delete(entries, k)
		folded = append(folded, k+"→"+others[0])
	}
	return namesDiag("info", "routes through an unlisted container folded into the API's one collection of that name", folded)
}

// versionTailRe is the end of a versioned schema-name prefix
// ("googlecloudrunv2", "widgetsv1beta1").
var versionTailRe = regexp.MustCompile(`v\d+(alpha|beta)?\d*$`)

// sameElement: some element of a is some element of b up to a version
// prefix (GoogleCloudRunV2Job and Job). Any other prefix names another
// schema: PublisherModel is not Model.
func sameElement(a, b map[string]bool) bool {
	prefixed := func(long, short string) bool {
		return strings.HasSuffix(long, short) && versionTailRe.MatchString(strings.TrimSuffix(long, short))
	}
	for x := range a {
		for y := range b {
			ix, iy := sdkinv.Ident(x), sdkinv.Ident(y)
			if ix == iy || prefixed(ix, iy) || prefixed(iy, ix) {
				return true
			}
		}
	}
	return false
}

// resolveParents points each entry at the collection whose item path is its
// innermost non-scope parent, else the next one out. The document tree nests
// nodes that list nothing (appengine/apps), so a parent may not be listable;
// then Parent stays empty while Depth keeps the template's count.
func resolveParents(entries map[string]*entry, items map[string]map[string]string) []sdkinv.Diagnostic {
	var missed []string
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		en := entries[k]
		if en.depth == 0 {
			continue
		}
		for _, pp := range en.parents {
			if p := items[en.api][pp]; p != "" && p != k {
				en.parent = p
				break
			}
		}
		if en.parent == "" {
			missed = append(missed, k)
		}
	}
	return namesDiag("info", "collections whose parents list nothing (Parent empty, Depth kept)", missed)
}

// dropAliasDocs removes the entries of every document name that shares a
// service identity with another: the name matching the rootUrl's own host
// label wins, else the alphabetically first. Returns the names dropped; their
// candidate ops move to byAPI so they drop with the rest of the document.
func dropAliasDocs(entries map[string]*entry, identities map[string][]string, byAPI map[string][]sdkinv.Operation) []string {
	dropped := map[string]bool{}
	for _, id := range slices.Sorted(maps.Keys(identities)) {
		names := slices.Compact(slices.Sorted(slices.Values(identities[id])))
		if len(names) < 2 {
			continue
		}
		host, _, _ := strings.Cut(strings.TrimPrefix(strings.SplitN(id, "\x00", 2)[0], "https://"), ".")
		keep := names[0]
		if slices.Contains(names, host) {
			keep = host
		}
		for _, n := range names {
			if n != keep {
				dropped[n] = true
			}
		}
	}
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		if en := entries[k]; dropped[en.api] {
			for _, ok := range slices.Sorted(maps.Keys(en.ops)) {
				byAPI[en.api] = append(byAPI[en.api], en.ops[ok])
			}
			delete(entries, k)
		}
	}
	return slices.Sorted(maps.Keys(dropped))
}

// aggLister is one aggregatedList call and the element it enumerates.
type aggLister struct {
	key, element, opKey, leaf string
	op                        sdkinv.Operation
}

// attachAggregated registers an aggregatedList on every sibling collection of
// the same document and scope that enumerates the same element: the response
// is a map of scoped lists, so the regional twin is listed by that one call.
func attachAggregated(entries map[string]*entry, elementOf map[string]string, aggregated []aggLister) {
	for _, a := range aggregated {
		for _, key := range slices.Sorted(maps.Keys(elementOf)) {
			if key == a.key || elementOf[key] != a.element {
				continue
			}
			en := entries[key]
			// A project's aggregated list never returns the org- or
			// folder-level twin (hierarchical firewall policies).
			if en == nil || !en.signals["scope:"+string(a.op.Scope)] {
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
	c.Signals = slices.Sorted(maps.Keys(en.signals))
	for _, k := range slices.Sorted(maps.Keys(en.ops)) {
		c.Ops = append(c.Ops, en.ops[k])
	}
	c.Refs = slices.Sorted(maps.Keys(en.refs))
	return c
}
