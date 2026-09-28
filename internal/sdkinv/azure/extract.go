package azure

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// builder is one generated <op>CreateRequest method: the ARM path template
// and HTTP verb the SDK sends for that operation.
type builder struct {
	client string // receiver type without the "Client" suffix
	op     string // exported operation name
	method string // GET, PUT, ...
	path   string
	module string // "<rp>/arm<mod>"
	paged  bool   // a New<op>Pager wrapper exists
	result string // list-result model the response decoder fills
}

var (
	// Receiver is "<X>Client" or the bare "Client" (armresources' generic
	// client, armmanagementgroups); the capture is empty for the latter.
	// The builder is unexported; an exported method may itself end in
	// CreateRequest (hdinsight ValidateClusterCreateRequest) and is no builder.
	builderRe = regexp.MustCompile(`(?m)^func \(client \*(\w*)Client\) ([a-z]\w*)CreateRequest\(`)
	// publicRe is every exported client method: the operations a caller sees.
	publicRe  = regexp.MustCompile(`(?m)^func \(client \*(\w*)Client\) ([A-Z]\w*)\(`)
	pagerRe   = regexp.MustCompile(`^New(\w+)Pager$`)
	urlPathRe = regexp.MustCompile(`urlPath := "([^"]+)"`)
	methodRe  = regexp.MustCompile(`http\.Method(\w+)`)
)

// scopeNames are static segments whose following {param} is a container the
// caller enumerates within, never a parent resource. Lowercase keys.
// "providers" joins them for the generic form
// ".../providers/{resourceProviderNamespace}/features", whose remainder would
// otherwise key microsoft.features/providers/features.
var scopeNames = map[string]bool{
	"subscriptions": true, "resourcegroups": true, "locations": true, "managementgroups": true, "providers": true,
}

// armOwnModules are the modules whose paths have no /providers/ segment
// because they address ARM's own namespace (resource groups, subscriptions).
var armOwnModules = map[string]bool{"resources/armresources": true, "resources/armsubscriptions": true, "subscription/armsubscription": true}

const armOwnNamespace = "microsoft.resources"

type entry struct {
	key         string
	namespace   string
	statics     []string
	parents     []string
	scope       sdkinv.Scope
	collection  []builder // GET on the collection path
	itemMethods map[string]bool
	alternate   bool // holds a lister folded in from an alternate path
	// viaLocation / viaOther record how this collection's listers reach it:
	// through a "locations/{location}" pair, which the key strips and ARM
	// keeps in the type name, or by some other path.
	viaLocation, viaOther bool
}

func (extractor) Extract(_ context.Context, dir string) (*sdkinv.Universe, error) {
	root := filepath.Join(dir, "repo", "sdk", "resourcemanager")
	u := &sdkinv.Universe{Provider: "azure", Pins: map[string]string{"azure-sdk-for-go": sdkinv.AzureSDKRef}}
	entries := map[string]*entry{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		cut := strings.LastIndex(rel, "/")
		if cut < 0 {
			return nil // a file directly under sdk/resourcemanager names no module
		}
		module := rel[:cut]
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		builders, unparsed := parseBuilders(string(raw), module)
		src, drops := accountPublic(string(raw), module, builders, unparsed)
		u.SourceOps = append(u.SourceOps, src...)
		u.Dropped = append(u.Dropped, drops...)
		for _, b := range builders {
			if !index(entries, b) {
				u.Other = append(u.Other, opFor(b, "", nil, ""))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	loaded := map[string]models{}
	modelsFor := func(module string) models {
		m, seen := loaded[module]
		if !seen {
			m = loadModels(root, module)
			loaded[module] = m
		}
		return m
	}
	foldAlternateListers(entries, func(b builder) string {
		if b.result == "" {
			return ""
		}
		return elementOf(modelsFor(b.module), b.result)
	})
	for _, e := range entries {
		c, ok := classify(e, func() bool {
			for _, b := range e.collection {
				if b.result != "" && armEnvelope(modelsFor(b.module), elementOf(modelsFor(b.module), b.result)) {
					return true
				}
			}
			return false
		})
		if !ok {
			continue
		}
		for _, b := range e.collection {
			if b.result == "" {
				continue
			}
			c.Refs = mergeRefs(c.Refs, refsOf(modelsFor(b.module), b.result))
		}
		u.Candidates = append(u.Candidates, c)
	}
	sdkinv.SortCandidates(u.Candidates)
	sdkinv.SortOps(u.Other)
	sdkinv.SortOpRefs(u.SourceOps)
	sdkinv.SortDrops(u.Dropped)
	u.Scopes = slices.Clone(scopes)
	return u, nil
}

// parseBuilders extracts every request builder in one generated client file.
// A builder with no literal urlPath or HTTP method is returned in unparsed.
func parseBuilders(src, module string) (out, unparsed []builder) {
	locs := builderRe.FindAllStringSubmatchIndex(src, -1)
	out = make([]builder, 0, len(locs))
	var results map[string]string
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := src[loc[1]:end]
		client, op := src[loc[2]:loc[3]], sdkinv.UpperFirst(src[loc[4]:loc[5]])
		if results == nil {
			results = parseResults(src)
		}
		pm := urlPathRe.FindStringSubmatch(body)
		mm := methodRe.FindStringSubmatch(body)
		if pm == nil || mm == nil {
			unparsed = append(unparsed, builder{client: client, op: op, module: module})
			continue
		}
		out = append(out, builder{
			client: client, op: op, method: strings.ToUpper(mm[1]), path: pm[1], module: module,
			paged:  strings.Contains(src, "func (client *"+client+"Client) New"+op+"Pager("),
			result: results[op],
		})
	}
	return out, unparsed
}

// accountPublic enumerates the file's exported client methods apart from the
// builders (Begin<Op> and New<Op>Pager wrap <op>CreateRequest) and returns
// them as source ops, each named as its builder's operation. A method with no
// builder, or whose builder has no literal path or verb, is dropped. A builder
// no exported method reaches is left out of the source, so accounting fails on
// it rather than letting it pass unseen.
func accountPublic(src, module string, builders, unparsed []builder) ([]sdkinv.OpRef, []sdkinv.Drop) {
	key := func(client, op string) string { return client + "\x00" + sdkinv.Canon(op) }
	parsed := map[string]builder{}
	for _, b := range builders {
		parsed[key(b.client, b.op)] = b
	}
	bare := map[string]builder{}
	for _, b := range unparsed {
		bare[key(b.client, b.op)] = b
	}
	seen := map[string]bool{}
	var refs []sdkinv.OpRef
	var drops []sdkinv.Drop
	for _, m := range publicRe.FindAllStringSubmatch(src, -1) {
		client, name := m[1], strings.TrimPrefix(m[2], "Begin")
		if pm := pagerRe.FindStringSubmatch(name); pm != nil {
			name = pm[1]
		}
		k := key(client, name)
		if seen[k] {
			continue
		}
		seen[k] = true
		if b, ok := parsed[k]; ok {
			refs = append(refs, opFor(b, "", nil, "").Ref())
			continue
		}
		b, ok := bare[k]
		reason := "no-request-path"
		if !ok {
			b, reason = builder{client: client, op: name, module: module}, "no-request-builder"
		}
		op := opFor(b, "", nil, "")
		refs = append(refs, op.Ref())
		drops = append(drops, sdkinv.Drop{Op: op, Reason: reason})
	}
	return refs, drops
}

// index files one builder under its (namespace, type path) entry.
func index(entries map[string]*entry, b builder) bool {
	segs := sdkinv.ParseTemplate(b.path)
	// The last "providers" whose successor is static: the generic extension
	// form ends "…/providers/{resourceProviderNamespace}/features", and taking
	// the last one outright discarded Microsoft.Features for a "*" namespace.
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
	var namespace string
	var rest []sdkinv.Segment
	switch {
	case nsIdx >= 0:
		ns := segs[nsIdx+1]
		if ns.Param {
			// The namespace is whatever the caller passes
			// (".../providers/{resourceProviderNamespace}/resourceTypes"), so
			// there is no resource provider to key: "*/resourcetypes" matched
			// no type and no ARM registry entry.
			return false
		}
		namespace = strings.ToLower(ns.Text)
		rest = segs[nsIdx+2:]
	case armOwnModules[b.module]:
		namespace = armOwnNamespace
		rest = segs
	default:
		return false // action/operation path outside any resource provider; nothing to list
	}
	rp := sdkinv.StripScopes(sdkinv.MarkIDs(rest, SingletonIDs), scopeNames, nil)
	if len(rp.Statics) == 0 {
		return false
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
		return false
	}
	if b.method != "GET" {
		return false
	}
	sc := scopeOf(segs, nsIdx)
	if e.scope == "" || scopeRank(sc) > scopeRank(e.scope) {
		e.scope = sc
	}
	// Only when *every* lister reaches the collection through a location does
	// ARM keep "locations" in the type name; a sibling ListBySubscription
	// proves it does not.
	if locationScoped {
		e.viaLocation = true
	} else {
		e.viaOther = true
	}
	e.collection = append(e.collection, b)
	return true
}

// foldAlternateListers moves a lister with no item path onto the entry whose
// item path writes the same element model in the same module. ARM exposes the
// same resource under several parents — microsoft.sql/servers/replicationlinks
// lists the ReplicationLink that microsoft.sql/servers/databases/replicationlinks
// writes — and judged on its own path the alternate has no write verb, so it
// read as a non-resource. Entries are walked in key order so the winner is
// stable; the loser's builders become extra ops on the winner.
func foldAlternateListers(entries map[string]*entry, elementOfOp func(builder) string) {
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
		for _, b := range e.collection {
			if el := elementOfOp(b); el != "" {
				id := b.module + "\x00" + el
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
		for _, b := range e.collection {
			el := elementOfOp(b)
			if el == "" {
				continue
			}
			if p := primary[b.module+"\x00"+el]; p != nil && p != e && (target == nil || p.key < target.key) {
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

// opFor is the Operation record for one request builder.
func opFor(b builder, namespace string, parents []string, scope sdkinv.Scope) sdkinv.Operation {
	mod := b.module[strings.LastIndex(b.module, "/")+1:]
	clientName := b.client
	if clientName == "" {
		clientName = "Client"
	}
	return sdkinv.Operation{
		Service: namespace,
		Name:    b.client + "Client." + b.op,
		Label:   mod + ":" + clientName + "." + b.op,
		IsList:  namespace != "",
		Paged:   b.paged,
		Targets: parents,
		Scope:   scope,
		Path:    b.path,
		Module:  fmt.Sprintf("azure-sdk-for-go@%s/sdk/resourcemanager/%s", sdkinv.AzureSDKRef, b.module),
		Client:  b.client + "Client",
	}
}

// SingletonIDs are the static segments ARM spells the one instance of a
// singleton child with (.../blobServices/default). Reading one as a
// collection made the PUT on that exact path an item write on a path nothing
// listed: 114 rows were excluded as having no item path while the cache showed
// GET+PUT, and 55 more carried the segment inside their key, where no scanner
// could match it. It is a grammar rule about ARM ids, not a list of resources.
var SingletonIDs = map[string]bool{"default": true, "current": true}

// scopes is ARM's scope vocabulary, narrowest first.
var scopes = []sdkinv.Scope{
	sdkinv.ScopeResourceGroup, sdkinv.ScopeSubscription, sdkinv.ScopeManagementGroup, sdkinv.ScopeTenant, sdkinv.ScopeExtension,
}

// scopeRank orders scopes widest-first for the collapsed candidate.
func scopeRank(s sdkinv.Scope) int { return slices.Index(scopes, s) + 1 }

// scopeOf reads the scope from the whole template, not only the segments
// before the namespace: a managementGroups pair sits after the namespace in
// microsoft.management's own paths and read as tenant. Extension is decided on
// the prefix alone (nsIdx is the namespace's "providers", -1 for ARM's own
// modules), or the trailing providers pair of a features listing would claim it.
func scopeOf(segs []sdkinv.Segment, nsIdx int) sdkinv.Scope {
	sc := sdkinv.ScopeTenant
	for i, s := range segs {
		if s.Param && i == 0 {
			return sdkinv.ScopeExtension // "{scope}/providers/..." or "{resourceUri}/providers/..."
		}
		if s.Param || i+1 >= len(segs) || !segs[i+1].Param {
			continue // only "<scope>/{param}" pairs widen or narrow the scope
		}
		switch strings.ToLower(s.Text) {
		case "providers":
			// ".../providers/{parentProviderNamespace}/{type}/{name}/providers/<ns>/..."
			// attaches this resource to whatever the caller names.
			if i < nsIdx {
				return sdkinv.ScopeExtension
			}
		case "subscriptions":
			if scopeRank(sc) > scopeRank(sdkinv.ScopeSubscription) || sc == sdkinv.ScopeTenant {
				sc = sdkinv.ScopeSubscription
			}
		case "resourcegroups":
			sc = sdkinv.ScopeResourceGroup
		case "managementgroups":
			sc = sdkinv.ScopeManagementGroup
		}
	}
	return sc
}

// classify decides the class from the item-path verbs, with one exception: a
// collection whose element carries the ARM proxy-resource envelope is a
// resource even when the SDK exposes only a read on the item path. 517 of 572
// catalog rows
// carry that envelope and 434 are children — exactly what the AWS extractor
// calls a resource — so excluding them made the two providers' denominators
// rest on different rules and hid 504 real gaps. The envelope alone is not
// enough (microsoft.authorization/provideroperations and
// microsoft.advisor/metadata are genuine provider catalogs), so it counts only
// with a second discriminator: a child, or a scope narrower than the tenant.
func classify(e *entry, hasEnvelope func() bool) (sdkinv.Candidate, bool) {
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
		c.Parent = e.namespace + "/" + strings.Join(e.statics[:len(e.statics)-1], "/")
	}
	switch {
	case e.itemMethods["PUT"] || e.itemMethods["PATCH"] || e.itemMethods["DELETE"]:
		c.Class, c.Rule = sdkinv.ClassResource, "item-write"
	case (e.itemMethods["GET"] || e.itemMethods["HEAD"]) &&
		(len(e.parents) > 0 || ownedScope[e.scope]) && hasEnvelope():
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
	if e.viaLocation && !e.viaOther {
		c.Signals = append(c.Signals, stripedLocationSignal)
	}
	c.Signals = append(c.Signals, "scope:"+string(e.scope))
	sort.Strings(c.Signals)
	for _, b := range e.collection {
		c.Ops = append(c.Ops, opFor(b, e.namespace, e.parents, e.scope))
	}
	return c, true
}

// stripedLocationSignal marks a candidate whose path reached it through a
// "locations/{location}" pair. internal/providers/azure reads it to rebuild the
// ARM type name for the registry cross-check.
const stripedLocationSignal = "scope-pair:locations"

// ownedScope names the scopes a subscription's own resources live in. A
// tenant-wide or extension listing is the provider talking about itself.
var ownedScope = map[sdkinv.Scope]bool{
	sdkinv.ScopeSubscription:  true,
	sdkinv.ScopeResourceGroup: true,
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
