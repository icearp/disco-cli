package azureinventory

import (
	"context"
	"errors"
	"fmt"
	"go/scanner"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// scopeNames are static segments whose following {param} is a container the
// caller enumerates within, never a parent resource: ARM's scope vocabulary.
// Lowercase keys. "providers" joins them for the generic form
// ".../providers/{resourceProviderNamespace}/features", whose remainder would
// otherwise key microsoft.features/providers/features.
var scopeNames = map[string]bool{
	"subscriptions": true, "resourcegroups": true, "locations": true, "managementgroups": true, "providers": true,
}

// armNamespace owns every path that names no resource provider: resource
// groups, subscriptions, deployments, tags are ARM's own types.
const armNamespace = "microsoft.resources"

type entry struct {
	key         string
	namespace   string
	statics     []string
	parents     []string
	scope       sdkinv.Scope
	collection  []listed // GETs on the collection path
	itemMethods map[string]bool
	alternate   bool     // holds a lister folded in from an alternate path
	singles     []listed // GETs on the collection path answering one model
	// singleAnswer: the listers are single-model GETs promoted because the
	// collection is written and nothing else lists it.
	singleAnswer bool
	// viaLocation / viaOther record how this collection's listers reach it:
	// through a "locations/{location}" pair, which the key strips and ARM
	// keeps in the type name, or by some other path.
	viaLocation, viaOther bool
}

// listed is one collection GET, the module that declares it, and how its
// path reaches the collection.
type listed struct {
	b           builder
	mod         *module
	scope       sdkinv.Scope
	viaLocation bool
}

func (l listed) element() string { return l.mod.shapeOf(l.b.result).element }

func (extractor) Extract(_ context.Context, dir string) (*sdkinv.Universe, error) {
	root := filepath.Join(dir, "repo", "sdk", "resourcemanager")
	u := &sdkinv.Universe{Provider: "azure", Pins: map[string]string{"azure-sdk-for-go": SDKRef}}
	mods, diags, err := parseModules(root)
	if err != nil {
		return nil, err
	}
	u.Diagnostics = append(u.Diagnostics, diags...)
	ids := instanceIDs(mods)
	entries := map[string]*entry{}
	for _, m := range mods {
		u.SourceOps = append(u.SourceOps, m.source...)
		u.Dropped = append(u.Dropped, m.drops...)
		for _, b := range m.builders {
			if ok, why := index(entries, m, b, ids); !ok {
				u.Other = append(u.Other, opFor(b, "", nil, ""))
				if why != "" {
					u.Diagnostics = append(u.Diagnostics, sdkinv.Diagnostic{Severity: "info", Source: m.path, Message: fmt.Sprintf("%s %s: %s", b.method, b.path, why)})
				}
			}
		}
	}
	u.Other = append(u.Other, promoteSingles(entries)...)
	foldAlternateListers(entries)
	for _, e := range entries {
		c, ok := classify(e)
		if !ok {
			continue
		}
		for _, l := range e.collection {
			c.Refs = mergeRefs(c.Refs, l.mod.refsOf(l.element()))
		}
		u.Candidates = append(u.Candidates, c)
	}
	u.Diagnostics = append(u.Diagnostics, absentModules(root, linkedModules())...)
	sdkinv.SortCandidates(u.Candidates)
	sdkinv.SortOps(u.Other)
	sdkinv.SortOpRefs(u.SourceOps)
	sdkinv.SortDrops(u.Dropped)
	sort.SliceStable(u.Diagnostics, func(i, j int) bool {
		a, b := u.Diagnostics[i], u.Diagnostics[j]
		return a.Source+"\x00"+a.Message < b.Source+"\x00"+b.Message
	})
	u.Scopes = slices.Clone(scopes)
	return u, nil
}

// parseModules parses every module directory under root in parallel and
// returns them in path order.
func parseModules(root string) ([]*module, []sdkinv.Diagnostic, error) {
	var rels []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if strings.Contains(filepath.ToSlash(rel), "/") { // a file directly under a provider dir names no module
			rels = append(rels, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(rels)
	mods := make([]*module, len(rels))
	errs := make([]error, len(rels))
	next := make(chan int)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for i := range next {
				mods[i], errs[i] = parseModule(root, rels[i])
			}
		})
	}
	for i := range rels {
		next <- i
	}
	close(next)
	wg.Wait()
	out := mods[:0]
	var diags []sdkinv.Diagnostic
	for i, m := range mods {
		// A file go/parser rejects (a newer Go syntax than this build knows)
		// costs its module, named, not the whole universe.
		var syntax scanner.ErrorList
		if errors.As(errs[i], &syntax) {
			diags = append(diags, sdkinv.Diagnostic{Severity: "warn", Source: rels[i], Message: "module skipped: " + syntax.Error()})
			continue
		}
		if errs[i] != nil {
			return nil, nil, fmt.Errorf("azure module %s: %w", rels[i], errs[i])
		}
		if len(m.builders) > 0 || len(m.source) > 0 {
			out = append(out, m)
		}
	}
	return out, diags, nil
}

// split returns a path's namespace and the segments after it. The namespace
// follows the last "providers" whose successor is static: the generic
// extension form ends ".../providers/{resourceProviderNamespace}/features",
// and taking the last one outright discarded Microsoft.Features for a "*"
// namespace. A path naming no provider is ARM's own. nsIdx is the namespace's
// "providers" segment, -1 when there is none; why says what made the path
// unattributable.
func split(segs []sdkinv.Segment) (namespace string, rest []sdkinv.Segment, nsIdx int, why string) {
	nsIdx, anyProviders := -1, -1
	for i := len(segs) - 2; i >= 0; i-- {
		if segs[i].Param || !strings.EqualFold(segs[i].Text, "providers") {
			continue
		}
		if anyProviders < 0 {
			anyProviders = i
		}
		if !segs[i+1].Param {
			nsIdx = i
			break
		}
	}
	if nsIdx < 0 {
		nsIdx = anyProviders
	}
	if nsIdx < 0 {
		return armNamespace, segs, -1, ""
	}
	if segs[nsIdx+1].Param {
		// The namespace is whatever the caller passes
		// (".../providers/{resourceProviderNamespace}/resourceTypes").
		return "", nil, nsIdx, "namespace is a parameter"
	}
	return strings.ToLower(segs[nsIdx+1].Text), segs[nsIdx+2:], nsIdx, ""
}

// itemRead is a GET answering one model: it reads an instance, never lists.
func itemRead(m *module, b builder) bool {
	return b.method == "GET" && !b.paged && m.shapeOf(b.result).model != ""
}

func writes(b builder) bool { return b.method == "PUT" || b.method == "PATCH" || b.method == "DELETE" }

// instanceIDs finds the static segments that are an instance's id rather
// than another collection (".../sites/{name}/config/web",
// ".../blobServices/default", ".../billingAccounts/default/..."). A static S
// after a static C is an id when S is not listed as a collection itself and
// either the SDK also spells C/{param} at that place, or it addresses C/S as
// an instance (a write, or a GET answering one model) while C itself is not
// one: runbooks/{r}/draft is a singleton, so draft/testJob is the test job,
// not the id of a draft. Keys are normTemplate forms of the path up to S; a
// longer path through the same prefix carries the same id.
func instanceIDs(mods []*module) map[string]bool {
	params := map[string]bool{}  // prefixes ending in a param
	listers := map[string]bool{} // static-final paths with a lister GET
	items := map[string]bool{}   // static-final paths addressed as an instance
	posts := map[string]bool{}   // static-final paths a POST addresses
	read := map[string]bool{}    // static-final paths some other verb addresses
	for _, m := range mods {
		for _, b := range m.builders {
			segs := sdkinv.ParseTemplate(b.path)
			for i, s := range segs {
				if s.Param {
					params[normTemplate(segs[:i+1])] = true
				}
			}
			if n := len(segs); n > 0 && !segs[n-1].Param {
				if b.method == "POST" {
					posts[normTemplate(segs)] = true
				} else {
					read[normTemplate(segs)] = true
				}
				switch {
				case writes(b) || itemRead(m, b):
					items[normTemplate(segs)] = true
				case b.method == "GET":
					listers[normTemplate(segs)] = true
				}
			}
		}
	}
	ids := map[string]bool{}
	for _, m := range mods {
		for _, b := range m.builders {
			segs := sdkinv.ParseTemplate(b.path)
			_, _, nsIdx, why := split(segs)
			if why != "" {
				continue
			}
			for i := restStart(nsIdx) + 1; i < len(segs); i++ {
				if segs[i].Param || segs[i-1].Param {
					continue
				}
				c, cs := normTemplate(segs[:i]), normTemplate(segs[:i+1])
				// A POST-only static beside C/{param} is an action
				// (checkNameAvailability), not an instance.
				sibling := params[c+"/{}"] && (read[cs] || !posts[cs])
				if listers[cs] || !sibling && (!items[cs] || items[c]) {
					continue
				}
				ids[cs] = true
			}
		}
	}
	return ids
}

// markIDs rewrites each static that ends an instanceIDs path prefix into a
// param. Only segments after the namespace are candidates, and only one that
// follows another static.
func markIDs(segs []sdkinv.Segment, restAt int, ids map[string]bool) []sdkinv.Segment {
	out := segs
	for i := restAt + 1; i < len(segs); i++ {
		if segs[i].Param || segs[i-1].Param || !ids[normTemplate(segs[:i+1])] {
			continue
		}
		if &out[0] == &segs[0] {
			out = slices.Clone(segs)
		}
		out[i].Param = true
	}
	return out
}

// restStart is where the resource path begins: after "providers/<ns>", or at
// the root of a path naming no provider.
func restStart(nsIdx int) int {
	if nsIdx < 0 {
		return 0
	}
	return nsIdx + 2
}

func normTemplate(segs []sdkinv.Segment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteByte('/')
		if s.Param {
			b.WriteString("{}")
		} else {
			b.WriteString(strings.ToLower(s.Text))
		}
	}
	return b.String()
}

// index files one builder under its (namespace, type path) entry and reports
// whether it lists that entry's collection. why is set when the path names no
// resource type at all.
func index(entries map[string]*entry, m *module, b builder, ids map[string]bool) (bool, string) {
	segs := sdkinv.ParseTemplate(b.path)
	namespace, _, nsIdx, why := split(segs)
	if why != "" {
		return false, why
	}
	restAt := restStart(nsIdx)
	segs = markIDs(segs, restAt, ids)
	rest := segs[restAt:]
	rp := sdkinv.StripScopes(rest, scopeNames, nil)
	if len(rp.Statics) == 0 {
		return false, "no resource type after the scopes"
	}
	// ARM keeps "locations" in the type name (Microsoft.Network/locations/…)
	// while the resource path spells it as a scope pair the key strips, so
	// record that it was there: without it 15 resource keys can never match
	// the registry and each one shows up as drift on both sides.
	locationScoped := false
	for i, sg := range rest {
		if !sg.Param && strings.EqualFold(sg.Text, "locations") && i+1 < len(rest) && rest[i+1].Param {
			locationScoped = true
			break
		}
	}
	statics := make([]string, len(rp.Statics))
	for i, s := range rp.Statics {
		statics[i] = strings.ToLower(s)
	}
	key := namespace + "/" + strings.Join(statics, "/")
	e := entries[key]
	if e == nil {
		parents := make([]string, len(rp.Parents))
		for i, p := range rp.Parents {
			parents[i] = strings.ToLower(p)
		}
		e = &entry{key: key, namespace: namespace, statics: statics, parents: parents, itemMethods: map[string]bool{}}
		entries[key] = e
	}
	if rp.Item {
		e.itemMethods[b.method] = true
		return false, ""
	}
	if b.method != "GET" {
		return false, ""
	}
	l := listed{b: b, mod: m, scope: scopeOf(segs, nsIdx), viaLocation: locationScoped}
	if itemRead(m, b) {
		// A GET answering one model reads an instance (…/{vm}/instanceView)
		// unless the collection has item writes and no other lister: then
		// the SDK types its lister as one entity (webapps
		// ListPremierAddOns), and it still finds the instances.
		e.singles = append(e.singles, l)
		return true, ""
	}
	e.addLister(l)
	return true, ""
}

func (e *entry) addLister(l listed) {
	if e.scope == "" || scopeRank(l.scope) > scopeRank(e.scope) {
		e.scope = l.scope
	}
	// Only when *every* lister reaches the collection through a location does
	// ARM keep "locations" in the type name; a sibling ListBySubscription
	// proves it does not.
	if l.viaLocation {
		e.viaLocation = true
	} else {
		e.viaOther = true
	}
	e.collection = append(e.collection, l)
}

// promoteSingles resolves each entry's single-model GETs: the listers of a
// written collection nothing else lists, else Other.
func promoteSingles(entries map[string]*entry) (other []sdkinv.Operation) {
	for _, e := range entries {
		written := e.itemMethods["PUT"] || e.itemMethods["PATCH"] || e.itemMethods["DELETE"]
		if written && len(e.collection) == 0 && len(e.singles) > 0 {
			for _, l := range e.singles {
				e.addLister(l)
			}
			e.singleAnswer = true
			continue
		}
		for _, l := range e.singles {
			other = append(other, opFor(l.b, "", nil, ""))
		}
	}
	return other
}

// foldAlternateListers moves a lister with no item path onto the entry whose
// item path writes the same element model in the same module. ARM exposes the
// same resource under several parents — microsoft.sql/servers/replicationlinks
// lists the ReplicationLink that microsoft.sql/servers/databases/replicationlinks
// writes — and judged on its own path the alternate has no write verb, so it
// read as a non-resource. Entries are walked in key order so the winner is
// stable; the loser's builders become extra ops on the winner.
func foldAlternateListers(entries map[string]*entry) {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	primary := map[string]*entry{} // module + element -> the entry that writes it
	for _, k := range keys {
		e := entries[k]
		if !e.itemMethods["PUT"] && !e.itemMethods["PATCH"] && !e.itemMethods["DELETE"] {
			continue
		}
		for _, l := range e.collection {
			if el := l.element(); el != "" {
				id := l.b.module + "\x00" + el
				if _, seen := primary[id]; !seen {
					primary[id] = e
				}
			}
		}
	}
	for _, k := range keys {
		e := entries[k]
		if len(e.itemMethods) > 0 || len(e.collection) == 0 {
			continue
		}
		var target *entry
		for _, l := range e.collection {
			el := l.element()
			if el == "" {
				continue
			}
			if p := primary[l.b.module+"\x00"+el]; p != nil && p != e && (target == nil || p.key < target.key) {
				target = p
			}
		}
		if target == nil {
			continue
		}
		target.collection = append(target.collection, e.collection...)
		target.alternate = true
		delete(entries, k)
	}
}

// opFor is the Operation record for one request builder. Labels drop the
// client type's "Client" suffix; the module's bare base client keeps it.
func opFor(b builder, namespace string, parents []string, scope sdkinv.Scope) sdkinv.Operation {
	mod := b.module[strings.LastIndex(b.module, "/")+1:]
	label := strings.TrimSuffix(b.client, "Client")
	if label == "" {
		label = b.client
	}
	return sdkinv.Operation{
		Service: namespace,
		Name:    b.client + "." + b.op,
		Label:   mod + ":" + label + "." + b.op,
		IsList:  namespace != "",
		Paged:   b.paged,
		Targets: parents,
		Scope:   scope,
		Path:    b.path,
		Module:  fmt.Sprintf("azure-sdk-for-go@%s/sdk/resourcemanager/%s", SDKRef, b.module),
		Client:  b.client,
	}
}

// scopes is ARM's scope vocabulary, narrowest first.
var scopes = []sdkinv.Scope{
	ScopeResourceGroup, ScopeSubscription, ScopeManagementGroup, ScopeTenant, ScopeExtension,
}

// scopeRank orders scopes widest-first for the collapsed candidate.
func scopeRank(s sdkinv.Scope) int { return slices.Index(scopes, s) + 1 }

// scopeOf reads the scope from the whole template, not only the segments
// before the namespace: a managementGroups pair sits after the namespace in
// microsoft.management's own paths and read as tenant. Extension is decided on
// the prefix alone (nsIdx is the namespace's "providers", -1 for ARM's own
// paths), or the trailing providers pair of a features listing would claim it.
func scopeOf(segs []sdkinv.Segment, nsIdx int) sdkinv.Scope {
	sc := ScopeTenant
	for i, s := range segs {
		if s.Param && i == 0 {
			return ScopeExtension // "{scope}/providers/..." or "{resourceUri}/providers/..."
		}
		if s.Param || i+1 >= len(segs) || !segs[i+1].Param {
			continue // only "<scope>/{param}" pairs widen or narrow the scope
		}
		switch strings.ToLower(s.Text) {
		case "providers":
			// ".../providers/{parentProviderNamespace}/{type}/{name}/providers/<ns>/..."
			// attaches this resource to whatever the caller names.
			if i < nsIdx {
				return ScopeExtension
			}
		case "subscriptions":
			if scopeRank(sc) > scopeRank(ScopeSubscription) || sc == ScopeTenant {
				sc = ScopeSubscription
			}
		case "resourcegroups":
			sc = ScopeResourceGroup
		case "managementgroups":
			sc = ScopeManagementGroup
		}
	}
	return sc
}

// classify decides the class from the item-path verbs, with one exception: a
// collection whose element carries the ARM proxy-resource envelope is a
// resource even when the SDK exposes only a read on the item path. 517 of 572
// catalog rows carry that envelope and 434 are children — exactly what the AWS
// extractor calls a resource — so excluding them made the two providers'
// denominators rest on different rules and hid 504 real gaps. The envelope
// alone is not enough (microsoft.authorization/provideroperations and
// microsoft.advisor/metadata are genuine provider catalogs), so it counts only
// with a second discriminator: a child, or a scope narrower than the tenant.
func classify(e *entry) (sdkinv.Candidate, bool) {
	if len(e.collection) == 0 {
		return sdkinv.Candidate{}, false
	}
	c := sdkinv.Candidate{
		Provider: "azure",
		Service:  e.namespace,
		Key:      e.key,
		Depth:    len(e.parents),
	}
	if c.Depth > 0 {
		c.Parent = e.namespace + "/" + strings.Join(e.statics[:parentEnd(e.statics, e.parents)], "/")
	}
	envelope := func() bool {
		return slices.ContainsFunc(e.collection, func(l listed) bool {
			el := l.element()
			return el != "" && l.mod.armEnvelope(el)
		})
	}
	locationOnly := e.viaLocation && !e.viaOther
	switch {
	case e.itemMethods["PUT"] || e.itemMethods["PATCH"] || e.itemMethods["DELETE"]:
		c.Class, c.Rule = sdkinv.ClassResource, "item-write"
	// A collection reached only through locations/{l} is the provider's
	// regional catalog (versions, manifests), not the caller's resources.
	case (e.itemMethods["GET"] || e.itemMethods["HEAD"]) && !locationOnly &&
		(len(e.parents) > 0 || ownedScope[e.scope]) && envelope():
		c.Class, c.Rule = sdkinv.ClassResource, "arm-envelope"
	case e.itemMethods["GET"] || e.itemMethods["HEAD"]:
		c.Class, c.Rule = sdkinv.ClassCatalog, "item-read-only"
	default:
		c.Class, c.Rule = sdkinv.ClassNonResource, "no-item-path"
	}
	for m := range e.itemMethods {
		c.Signals = append(c.Signals, "item:"+m)
	}
	if len(e.itemMethods) == 0 {
		c.Signals = append(c.Signals, "no-item-path")
	}
	if e.alternate {
		c.Signals = append(c.Signals, "alternate-lister")
	}
	if e.singleAnswer {
		c.Signals = append(c.Signals, "single-answer-lister")
	}
	if locationOnly {
		c.Signals = append(c.Signals, LocationScopeSignal)
	}
	c.Signals = append(c.Signals, "scope:"+string(e.scope))
	sort.Strings(c.Signals)
	for _, l := range e.collection {
		c.Ops = append(c.Ops, opFor(l.b, e.namespace, e.parents, e.scope))
	}
	return c, true
}

// parentEnd is the length of the statics prefix that keys the parent: up to
// the innermost static followed by an id. Cutting only the last static gave
// runbooks/{r}/draft/testJob/streams the parent runbooks/draft/testjob, which
// is no collection and disagrees with Depth, which counts id-bearing statics.
func parentEnd(statics, parents []string) int {
	end, next := 0, 0
	for i, s := range statics {
		if next < len(parents) && s == parents[next] {
			end, next = i+1, next+1
		}
	}
	return end
}

// LocationScopeSignal marks a candidate whose path reached it through a
// "locations/{location}" pair. internal/providers/azure reads it to rebuild the
// ARM type name for the registry cross-check.
const LocationScopeSignal = "scope-pair:locations"

// ownedScope names the scopes a subscription's own resources live in. A
// tenant-wide or extension listing is the provider talking about itself.
var ownedScope = map[sdkinv.Scope]bool{
	ScopeSubscription:  true,
	ScopeResourceGroup: true,
}

func mergeRefs(a, b []string) []string {
	if len(b) == 0 {
		return a
	}
	seen := map[string]bool{}
	for _, r := range a {
		seen[r] = true
	}
	for _, r := range b {
		if !seen[r] {
			seen[r] = true
			a = append(a, r)
		}
	}
	sort.Strings(a)
	return a
}
