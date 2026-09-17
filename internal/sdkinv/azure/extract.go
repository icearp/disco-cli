package azure

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	builderRe = regexp.MustCompile(`(?m)^func \(client \*(\w*)Client\) (\w+)CreateRequest\(`)
	urlPathRe = regexp.MustCompile(`urlPath := "([^"]+)"`)
	methodRe  = regexp.MustCompile(`http\.Method(\w+)`)
)

// scopeNames are static segments whose following {param} is a container the
// caller enumerates within, never a parent resource. Lowercase keys.
var scopeNames = map[string]bool{
	"subscriptions": true, "resourcegroups": true, "locations": true, "managementgroups": true,
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
		module := rel[:strings.LastIndex(rel, "/")]
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, b := range parseBuilders(string(raw), module) {
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
	for _, e := range entries {
		c, ok := classify(e)
		if !ok {
			continue
		}
		for _, b := range e.collection {
			if b.result == "" {
				continue
			}
			m, seen := loaded[b.module]
			if !seen {
				m = loadModels(root, b.module)
				loaded[b.module] = m
			}
			c.Refs = mergeRefs(c.Refs, refsOf(m, b.result))
		}
		u.Candidates = append(u.Candidates, c)
	}
	sdkinv.SortCandidates(u.Candidates)
	sdkinv.SortOps(u.Other)
	return u, nil
}

// parseBuilders extracts every request builder in one generated client file.
func parseBuilders(src, module string) []builder {
	locs := builderRe.FindAllStringSubmatchIndex(src, -1)
	out := make([]builder, 0, len(locs))
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
			continue
		}
		out = append(out, builder{
			client: client, op: op, method: strings.ToUpper(mm[1]), path: pm[1], module: module,
			paged:  strings.Contains(src, "func (client *"+client+"Client) New"+op+"Pager("),
			result: results[op],
		})
	}
	return out
}

// index files one builder under its (namespace, type path) entry.
func index(entries map[string]*entry, b builder) bool {
	segs := sdkinv.ParseTemplate(b.path)
	nsIdx := -1
	for i := len(segs) - 2; i >= 0; i-- {
		if !segs[i].Param && strings.EqualFold(segs[i].Text, "providers") {
			nsIdx = i
			break
		}
	}
	var namespace string
	var prefix, rest []sdkinv.Segment
	switch {
	case nsIdx >= 0:
		ns := segs[nsIdx+1]
		if ns.Param {
			namespace = "*"
		} else {
			namespace = strings.ToLower(ns.Text)
		}
		prefix, rest = segs[:nsIdx], segs[nsIdx+2:]
	case armOwnModules[b.module]:
		namespace = armOwnNamespace
		prefix, rest = segs, segs
	default:
		return false // action/operation path outside any resource provider; nothing to list
	}
	rp := sdkinv.StripScopes(rest, scopeNames, nil)
	if len(rp.Statics) == 0 {
		return false
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
	sc := scopeOf(prefix, namespace)
	if e.scope == "" || scopeRank[sc] > scopeRank[e.scope] {
		e.scope = sc
	}
	e.collection = append(e.collection, b)
	return true
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
	}
}

// scopeRank orders scopes widest-first for the collapsed candidate.
var scopeRank = map[sdkinv.Scope]int{
	sdkinv.ScopeExtension: 5, sdkinv.ScopeTenant: 4, sdkinv.ScopeManagementGroup: 3, sdkinv.ScopeSubscription: 2, sdkinv.ScopeResourceGroup: 1,
}

func scopeOf(prefix []sdkinv.Segment, namespace string) sdkinv.Scope {
	if namespace == "*" {
		return sdkinv.ScopeExtension
	}
	sc := sdkinv.ScopeTenant
	for i, s := range prefix {
		if s.Param && i == 0 {
			return sdkinv.ScopeExtension // "{scope}/providers/..." or "{resourceUri}/providers/..."
		}
		if s.Param || i+1 >= len(prefix) || !prefix[i+1].Param {
			continue // only "<scope>/{param}" pairs widen or narrow the scope
		}
		switch strings.ToLower(s.Text) {
		case "subscriptions":
			if scopeRank[sc] > scopeRank[sdkinv.ScopeSubscription] || sc == sdkinv.ScopeTenant {
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
		c.Parent = e.namespace + "/" + strings.Join(e.statics[:len(e.statics)-1], "/")
	}
	switch {
	case e.itemMethods["PUT"] || e.itemMethods["PATCH"] || e.itemMethods["DELETE"]:
		c.Class = sdkinv.ClassResource
	case e.itemMethods["GET"] || e.itemMethods["HEAD"]:
		c.Class = sdkinv.ClassCatalog
	default:
		c.Class = sdkinv.ClassNonResource
	}
	for m := range e.itemMethods {
		c.Signals = append(c.Signals, "item:"+m)
	}
	if len(e.itemMethods) == 0 {
		c.Signals = append(c.Signals, "no-item-path")
	}
	c.Signals = append(c.Signals, "scope:"+string(e.scope))
	sort.Strings(c.Signals)
	for _, b := range e.collection {
		c.Ops = append(c.Ops, opFor(b, e.namespace, e.parents, e.scope))
	}
	return c, true
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
