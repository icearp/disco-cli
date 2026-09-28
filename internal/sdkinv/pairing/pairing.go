// Package pairing walks a provider's scanner source with go/ast and pairs the
// SDK operations it calls (anchors) with the disco types it stores, checked
// against the SDK-derived universe. Anchors are authoritative; op labels in
// string literals are a cross-check. No x/tools: parsing only, no type-check.
package pairing

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// Anchor is one SDK call site: the universe module key and operation name
// the provider resolver recognised.
type Anchor struct {
	Module string
	Op     string
	Line   int
}

// Pairing is one operation used by one scanner function and the types that
// function stores.
type Pairing struct {
	Provider string   `json:"provider"`
	Service  string   `json:"service"`
	Key      string   `json:"key"` // candidate key; "" for an "other" op
	Op       string   `json:"op"`
	Label    string   `json:"label"`
	Types    []string `json:"types"`
	Labels   []string `json:"labels"` // op labels in the same function that resolve to Op
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Func     string   `json:"func"`
	Kind     string   `json:"kind"` // emits | sidecar | derived | label | other (a non-candidate op) | skew
}

// LooseLabeller is the optional half of the label grammar: a shape that is
// recognisably an op label for this provider but does not satisfy
// LabelGrammar. Without it an off-grammar literal is simply invisible, so a
// typo reads as "this function names no op" rather than as a wrong label.
type LooseLabeller interface {
	LooseLabelGrammar() *regexp.Regexp
}

// Diagnostic is a pairing problem the strict test fails on.
type Diagnostic struct {
	Kind    string `json:"kind"` // label-no-op | label-no-anchor | label-malformed | op-not-in-inventory | unresolved-receiver
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// Result is the pairing of one scanner package against one universe.
type Result struct {
	Provider    string
	Pairings    []Pairing
	Diagnostics []Diagnostic
	// Types maps every string constant declared in the package to the files
	// referencing it outside init; a type with no pairing is reported by
	// Unpaired.
	Consts map[string]string
	// StoredBy names, per type value, the functions that build or write its
	// rows. An unexplained type is a scanner whose store sits out of the
	// walker's reach, and this is the only thing that says which one.
	StoredBy map[string][]string
}

// Func is the per-function view a Resolver inspects.
type Func struct {
	fset    *token.FileSet
	Decl    *ast.FuncDecl
	Imports map[string]string // local name -> import path
	// Vars maps a local variable to the (module, ident) it was built from:
	// "client" -> {armcompute, VirtualMachinesClient}; "svc" -> {compute, Service}.
	Vars map[string]Binding
	// Fields maps the receiver struct's SDK-typed fields to their origin
	// ("svc" -> {compute, Service}), for s.svc.X.List(. Empty for functions.
	Fields map[string]Binding
	// SDKMods lists the module keys the file imports, in import order.
	SDKMods []string
	// Ops answers whether the universe has (module, op) as a candidate op;
	// Other whether it ships it as a non-candidate op.
	Ops   func(module, op string) bool
	Other func(module, op string) bool
	// ClientsWith lists the modules' idents (clients) exposing op, for
	// resolving a receiver by uniqueness.
	ClientsWith func(module, op string) []string
}

// Binding is where a local came from.
type Binding struct {
	Module string
	Ident  string // client or service type name
}

// Resolver is the per-provider grammar: how imports map to universe modules,
// how universe ops key, and which call shapes are SDK anchors.
type Resolver interface {
	Name() string
	LabelGrammar() *regexp.Regexp
	// ImportKey maps an SDK import path to a universe module key; "" for
	// imports that are not SDK operation packages.
	ImportKey(path string) string
	// OpKey maps a universe operation to (module key, op key) for anchor lookup.
	OpKey(op sdkinv.Operation) (module, name string)
	// LabelAliases lists every label literal form that names op.
	LabelAliases(c sdkinv.Candidate, op sdkinv.Operation) []string
	// Constructor reports whether a call ident on module (NewXClient,
	// NewService) binds a local to (module, client ident).
	Constructor(module, fn string) (ident string, ok bool)
	// TypeOwner reports the client ident a module type belongs to
	// (armquota.ClientListResponse -> Client), so a local interface seam whose
	// methods mention that type binds its parameters like the client itself.
	TypeOwner(module, typeName string) (ident string, ok bool)
	// LabelOp maps a label literal to the operation name it spells, in the
	// form an Anchor's Op carries, so a label naming a call the pinned SDK no
	// longer has reads as skew rather than a typo.
	LabelOp(lit string) string
	Anchors(f *Func) ([]Anchor, []Diagnostic)
}

var (
	mu        sync.RWMutex
	resolvers = map[string]Resolver{}
)

// Register adds a provider resolver; called from init. Duplicate names
// panic, mirroring sdkinv.Register.
func Register(r Resolver) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := resolvers[r.Name()]; dup {
		panic(fmt.Sprintf("pairing: duplicate resolver %q", r.Name()))
	}
	resolvers[r.Name()] = r
}

// Get returns the resolver for a provider.
func Get(name string) (Resolver, bool) {
	mu.RLock()
	defer mu.RUnlock()
	r, ok := resolvers[name]
	return r, ok
}

const calleeDepth = 3

type fn struct {
	name     string
	file     string
	line     int
	decl     *ast.FuncDecl
	types    map[string]bool
	outflow  map[string]map[string]bool // callee -> type constants passed to it as arguments
	inflow   map[string]map[string]bool // caller -> type constants it passes in
	labels   map[string][]int           // label literal -> lines
	near     map[string][]int           // off-grammar literals of a labelish shape
	callees  map[string]bool
	feeds    map[string]bool // callees this body hands something it produced
	forwards map[string]bool // callees this body hands one of its own parameters
	carries  bool            // some caller feeds this function
	stores   bool            // this body writes resource rows
	anchors  []Anchor
	diags    []Diagnostic
	mods     []string // SDK module keys the file imports
	callers  []*fn

	storesReach bool // this body, or a callee, writes resource rows

	rtypes   []string                    // types of this function and the callees it feeds
	otypes   []string                    // rtypes minus types only a caller passes in
	atypes   []string                    // every reachable callee's types, fed or not
	anchored map[string]opRef            // candidate key -> op, from own anchors
	other    map[string]sdkinv.Operation // non-candidate ops, by label
	missing  []string                    // bound SDK calls the pinned SDK no longer has
	reach    map[string]opRef            // anchored by this function or its callees
	typeless map[string]bool             // reach keys a direct callee anchors without storing
}

// Walk parses every non-test .go file in dir and pairs it against u.
func Walk(dir string, u *sdkinv.Universe) (*Result, error) {
	r, ok := Get(u.Provider)
	if !ok {
		return nil, fmt.Errorf("pairing: no resolver for provider %q", u.Provider)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, perr := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil, perr
		}
		parsed = append(parsed, af)
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("pairing: no Go files under %s", dir)
	}
	var loose *regexp.Regexp
	if l, ok := r.(LooseLabeller); ok {
		loose = l.LooseLabelGrammar()
	}
	idx := indexUniverse(r, u)
	consts := collectConsts(parsed, u.Provider)
	varTypes := collectVarTypes(parsed, consts)
	fields := collectFields(parsed, r)
	seams := collectSeams(parsed, r)
	res := &Result{Provider: u.Provider, Consts: consts, StoredBy: map[string][]string{}}
	fns := map[string]*fn{}
	var order []*fn
	for _, af := range parsed {
		imports, mods := fileImports(r, af)
		file := filepath.Base(fset.Position(af.Pos()).Filename)
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			f := &fn{
				name: funcName(fd), file: file, line: fset.Position(fd.Pos()).Line, decl: fd,
				types: map[string]bool{}, outflow: map[string]map[string]bool{}, inflow: map[string]map[string]bool{},
				labels: map[string][]int{}, near: map[string][]int{}, callees: map[string]bool{}, feeds: map[string]bool{}, forwards: map[string]bool{}, mods: mods,
			}
			scanBody(f, fd, fset, consts, varTypes, imports, r.LabelGrammar(), loose)
			fv := &Func{
				fset: fset, Decl: fd, Imports: imports, Vars: bindLocals(fd, imports, r, seams), Fields: fields[recvType(fd)], SDKMods: mods,
				Ops: idx.has, Other: idx.hasOther, ClientsWith: idx.clientsWith,
			}
			f.anchors, f.diags = r.Anchors(fv)
			for i := range f.diags {
				f.diags[i].File = file
			}
			fns[f.name] = f
			order = append(order, f)
			if fd.Recv != nil {
				k := methodKey(fd.Name.Name)
				if _, dup := fns[k]; dup {
					fns[k] = nil // ambiguous: several receivers share the method name
				} else {
					fns[k] = f
				}
			}
		}
	}
	linkFlows(fns, order)
	linkCallers(fns, order)
	linkFeeds(fns, order)
	markStoring(fns, order)
	for _, f := range order {
		f.rtypes = reachableTypes(fns, f, false, walkFedCallees)
		f.otypes = reachableTypes(fns, f, true, walkFedCallees)
		f.atypes = reachableTypes(fns, f, false, walkCallees)
		f.resolveAnchors(idx, res)
	}
	for _, f := range order {
		f.reach, f.typeless = reachableAnchors(fns, f)
	}
	for _, f := range order {
		if f.name != "init" {
			f.emit(fns, idx, res, u.Provider)
		}
	}
	for _, f := range order {
		if !f.stores {
			continue
		}
		for t := range f.types {
			res.StoredBy[t] = append(res.StoredBy[t], f.name)
		}
	}
	for t := range res.StoredBy {
		sort.Strings(res.StoredBy[t])
		res.StoredBy[t] = slices.Compact(res.StoredBy[t])
	}
	res.Pairings = dropEmittedFromDerived(res.Pairings)
	sort.Slice(res.Diagnostics, func(i, j int) bool {
		a, b := res.Diagnostics[i], res.Diagnostics[j]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
	return res, nil
}

// resolveAnchors sorts the function's own SDK calls into candidate ops,
// non-candidate ops and calls the pinned SDK no longer has.
func (f *fn) resolveAnchors(idx *index, res *Result) {
	f.anchored = map[string]opRef{}
	f.other = map[string]sdkinv.Operation{}
	for _, a := range f.anchors {
		if ref, ok := idx.byKey[a.Module+"\x00"+a.Op]; ok {
			f.anchored[ref.key] = ref
			continue
		}
		if op, ok := idx.other[a.Module+"\x00"+a.Op]; ok {
			f.other[op.Label] = op
			continue
		}
		f.missing = append(f.missing, a.Op)
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Kind: "sdk-skew", File: f.file, Line: a.Line,
			Message: fmt.Sprintf("%s.%s is not in %s at the pinned ref", a.Module, a.Op, a.Module),
		})
	}
}

// emit writes the function's pairings and label diagnostics. Labels are
// checked against SDK calls in the function, the helpers it calls and its
// direct callers: the label sits at the error site, the call may sit in a
// paging helper or in the caller that built the pager.
func (f *fn) emit(fns map[string]*fn, idx *index, res *Result, provider string) {
	res.Diagnostics = append(res.Diagnostics, f.diags...)
	types := f.rtypes
	if !f.storesReach {
		f.otypes = nil
		f.atypes = nil
		// Naming a type is not storing it. Without a store call the anchor is
		// still real, so the pairing stands as a sidecar and the candidate
		// stays covered; only the credited types go.
		types = nil
	}
	for _, op := range f.missing {
		res.Pairings = append(res.Pairings, Pairing{
			Provider: provider, Op: op, Label: op,
			Types: types, File: f.file, Line: f.line, Func: f.name, Kind: "skew",
		})
	}
	for _, label := range sortedOps(f.other) {
		op := f.other[label]
		res.Pairings = append(res.Pairings, Pairing{
			Provider: provider, Service: op.Service, Op: op.Name, Label: op.Label,
			Types: types, File: f.file, Line: f.line, Func: f.name, Kind: "other",
		})
	}
	anchored := map[string]opRef{}
	for k, v := range f.anchored {
		anchored[k] = v
	}
	// A helper that only lists (a pager builder returning rows to its
	// caller) is paired with the types its caller stores.
	promoted := map[string]bool{}
	for k, v := range f.reach {
		if f.typeless[k] && len(types) > 0 {
			anchored[k] = v
			promoted[k] = true
		}
	}
	callerReach := map[string]opRef{}
	missing := f.missing
	for _, c := range f.callers {
		for k, v := range c.reach {
			callerReach[k] = v
		}
		missing = append(missing, c.missing...)
	}
	// A dispatcher whose rows are built from a sibling's listing (broker
	// configuration associations from DescribeBroker) pairs the types no
	// anchored callee stores with everything its callees list.
	if len(anchored) == 0 && len(f.reach) > 0 && f.storesReach {
		if orphan := f.orphanTypes(fns); len(orphan) > 0 {
			for _, k := range sortedKeys(f.reach) {
				op := f.reach[k].op
				// Only the orphan types this listing could plausibly have
				// produced. Unfiltered, a dispatcher paired every orphan with
				// every key it reached.
				rel := relatedTypes(k, op, orphan)
				if len(rel) == 0 {
					continue
				}
				res.Pairings = append(res.Pairings, Pairing{
					Provider: provider, Service: op.Service, Key: k, Op: op.Name, Label: op.Label,
					Types: rel, File: f.file, Line: f.line, Func: f.name, Kind: "derived",
				})
			}
		}
	}
	reach := map[string]opRef{}
	for _, m := range []map[string]opRef{callerReach, f.reach, anchored} {
		for k, v := range m {
			reach[k] = v
		}
	}
	for _, lit := range sortedLines(f.near) {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Kind: "label-malformed", File: f.file, Line: f.near[lit][0],
			Message: fmt.Sprintf("label %q is not in this provider's label grammar", lit),
		})
	}
	labelOps := map[string][]string{} // candidate key -> label literals in this function
	labelOnly := map[string]bool{}
	for lit, lines := range f.labels {
		ref, ok := idx.resolveLabel(lit, f.mods, reach)
		if !ok {
			switch {
			case idx.known(lit, f.mods):
			case skewed(idx.r.LabelOp(lit), missing):
				res.Diagnostics = append(res.Diagnostics, Diagnostic{
					Kind: "sdk-skew", File: f.file, Line: lines[0],
					Message: fmt.Sprintf("label %q names an operation the pinned SDK no longer has", lit),
				})
			case moduleAbsent(lit, f.mods, idx.modules):
				res.Diagnostics = append(res.Diagnostics, Diagnostic{
					Kind: "sdk-module-absent", File: f.file, Line: lines[0],
					Message: fmt.Sprintf("label %q: the file's SDK module is not in the pinned SDK", lit),
				})
			default:
				res.Diagnostics = append(res.Diagnostics, Diagnostic{
					Kind: "label-no-op", File: f.file, Line: lines[0],
					Message: fmt.Sprintf("label %q names no %s operation", lit, provider),
				})
			}
			continue
		}
		labelOps[ref.key] = append(labelOps[ref.key], lit)
		if _, ok := reach[ref.key]; ok {
			continue
		}
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Kind: "label-no-anchor", File: f.file, Line: lines[0],
			Message: fmt.Sprintf("label %q has no matching SDK call in %s, its helpers or its callers", lit, f.name),
		})
		anchored[ref.key] = ref
		labelOnly[ref.key] = true
	}
	for _, key := range sortedKeys(anchored) {
		op := anchored[key].op
		// A promoted anchor belongs to a typeless listing helper, not to this
		// function, so it is credited only with what this function's own flow
		// stores. Types a *caller* hands in ride the cur == f inflow rule,
		// which is right for a helper that genuinely lists and stores and
		// wrong here: it gave microsoft.resources/resourcegroups five types
		// from unrelated callers of the same fan-out helper.
		ts := types
		if promoted[key] {
			ts = f.otypes
		}
		kind := "emits"
		switch {
		case labelOnly[key]:
			kind = "label"
		case len(ts) == 0:
			kind = "sidecar"
		}
		labels := labelOps[key]
		sort.Strings(labels)
		res.Pairings = append(res.Pairings, Pairing{
			Provider: provider, Service: op.Service, Key: key, Op: op.Name, Label: op.Label,
			Types: ts, Labels: labels, File: f.file, Line: f.line, Func: f.name, Kind: kind,
		})
	}
}

// Unpaired returns every string constant whose value looks like a disco type
// for this provider and that no pairing carries, with a reason: "non-sdk"
// when every file referencing it imports no SDK package, else "unexplained".
func (res *Result) Unpaired(consts map[string]string, sdkFiles map[string]bool) map[string]string {
	paired := map[string]bool{}
	derived := map[string]bool{}
	viaOther := map[string]string{}
	for _, p := range res.Pairings {
		for _, t := range p.Types {
			switch p.Kind {
			case "other":
				viaOther[t] = "other-op:" + p.Label
			case "skew":
				viaOther[t] = "sdk-skew:" + p.Label
			case "derived":
				// Evidence by proximity, weaker than a named op: a type the
				// SDK no longer has, or one stored from a non-candidate op,
				// keeps that honest reason instead.
				derived[t] = true
			case "label":
				// A label with no SDK call proves nothing, which is why
				// inventory.go's pairingKinds excludes it. Counting it as
				// paired here made the walker and the matrix report the same
				// type with two different reasons and pointed CI at the wrong
				// one.
			default:
				paired[t] = true
			}
		}
	}
	out := map[string]string{}
	for name, val := range consts {
		if !strings.HasPrefix(val, res.Provider+":") || paired[val] {
			continue
		}
		reason := "unexplained"
		switch {
		case viaOther[val] != "":
			reason = viaOther[val]
		case derived[val]:
			continue
		case !sdkFiles[name]:
			reason = "non-sdk"
		}
		out[val] = reason
	}
	return out
}

type opRef struct {
	op  sdkinv.Operation
	key string // candidate key
}

type index struct {
	r       Resolver
	byKey   map[string]opRef   // module\x00op
	byLabel map[string][]opRef // candidate ops by every alias form (several when the alias is ambiguous)
	other   map[string]sdkinv.Operation
	clients map[string][]string // module\x00op -> client idents
	modules map[string]bool
}

func indexUniverse(r Resolver, u *sdkinv.Universe) *index {
	idx := &index{r: r, byKey: map[string]opRef{}, byLabel: map[string][]opRef{}, other: map[string]sdkinv.Operation{}, clients: map[string][]string{}, modules: map[string]bool{}}
	for _, c := range u.Candidates {
		for _, op := range c.Ops {
			mod, name := r.OpKey(op)
			idx.modules[mod] = true
			idx.byKey[mod+"\x00"+name] = opRef{op, c.Key}
			for _, l := range r.LabelAliases(c, op) {
				idx.byLabel[l] = append(idx.byLabel[l], opRef{op, c.Key})
			}
			if method, ok := strings.CutPrefix(name, op.Client+"."); ok && op.Client != "" {
				idx.clients[mod+"\x00"+method] = append(idx.clients[mod+"\x00"+method], op.Client)
			}
		}
	}
	for _, op := range u.Other {
		mod, name := r.OpKey(op)
		idx.modules[mod] = true
		for _, l := range r.LabelAliases(sdkinv.Candidate{}, op) {
			idx.other[l] = op
		}
		idx.other[mod+"\x00"+name] = op
	}
	return idx
}

// resolveLabel finds the candidate a label literal names: an alias form,
// preferring a candidate the function anchors when the alias is shared
// ("pubsub:subscriptions.list" names both subscriptions and
// topics/subscriptions); else an op of that name in a module the file
// imports (shared AWS signing names: "apigatewayv2:GetApis" signs as
// apigateway).
func (idx *index) resolveLabel(lit string, mods []string, anchored map[string]opRef) (opRef, bool) {
	refs := idx.byLabel[lit]
	for _, ref := range refs {
		if _, ok := anchored[ref.key]; ok {
			return ref, true
		}
	}
	if keys := distinctKeys(refs); len(keys) == 1 {
		return refs[0], true
	}
	_, name, _ := strings.Cut(lit, ":")
	for _, mod := range mods {
		if ref, ok := idx.byKey[mod+"\x00"+name]; ok {
			return ref, true
		}
	}
	return opRef{}, false
}

func distinctKeys(refs []opRef) map[string]bool {
	out := map[string]bool{}
	for _, r := range refs {
		out[r.key] = true
	}
	return out
}

// known reports whether a label names a non-candidate op the SDK ships.
func (idx *index) known(lit string, mods []string) bool {
	if _, ok := idx.other[lit]; ok {
		return true
	}
	_, name, _ := strings.Cut(lit, ":")
	for _, mod := range mods {
		if _, ok := idx.other[mod+"\x00"+name]; ok {
			return true
		}
	}
	return false
}

func (idx *index) has(module, op string) bool {
	_, ok := idx.byKey[module+"\x00"+op]
	return ok
}

func (idx *index) hasOther(module, op string) bool {
	_, ok := idx.other[module+"\x00"+op]
	return ok
}

func (idx *index) clientsWith(module, op string) []string {
	return idx.clients[module+"\x00"+op]
}

func collectConsts(files []*ast.File, provider string) map[string]string {
	out := map[string]string{}
	for _, af := range files {
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if v, err := strconv.Unquote(lit.Value); err == nil && strings.HasPrefix(n.Name, "Type") && strings.HasPrefix(v, provider+":") {
								out[n.Name] = v
							}
						}
					}
				}
			}
		}
	}
	return out
}

// collectVarTypes maps each package-level var to the type constants its
// initialiser references, so a table-driven scanner ranging over the table
// stores what the table names.
func collectVarTypes(files []*ast.File, consts map[string]string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, af := range files {
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				types := map[string]bool{}
				for _, v := range vs.Values {
					ast.Inspect(v, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok {
							if t, ok := consts[id.Name]; ok {
								types[t] = true
							}
						}
						return true
					})
				}
				if len(types) == 0 {
					continue
				}
				for _, n := range vs.Names {
					out[n.Name] = types
				}
			}
		}
	}
	return out
}

// collectFields maps, per struct type, fields typed *mod.Ident (an SDK
// client or service) to their origin, so methods on a scanner struct resolve
// s.svc.X.List( and c.client.NewListPager(.
func collectFields(files []*ast.File, r Resolver) map[string]map[string]Binding {
	out := map[string]map[string]Binding{}
	for _, af := range files {
		imports, _ := fileImports(r, af)
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, sp := range gd.Specs {
				ts := sp.(*ast.TypeSpec)
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, fld := range st.Fields.List {
					sel, ok := unstar(fld.Type).(*ast.SelectorExpr)
					if !ok {
						continue
					}
					x, ok := sel.X.(*ast.Ident)
					if !ok {
						continue
					}
					mod := ""
					if path, isImport := imports[x.Name]; isImport {
						mod = r.ImportKey(path)
					}
					if mod == "" {
						continue
					}
					if out[ts.Name.Name] == nil {
						out[ts.Name.Name] = map[string]Binding{}
					}
					for _, n := range fld.Names {
						out[ts.Name.Name][n.Name] = Binding{Module: mod, Ident: sel.Sel.Name}
					}
				}
			}
		}
	}
	return out
}

// collectSeams maps every package-local interface type to the client whose
// types its method signatures mention (a scanner's narrow test seam).
func collectSeams(files []*ast.File, r Resolver) map[string]Binding {
	out := map[string]Binding{}
	for _, af := range files {
		imports, _ := fileImports(r, af)
		for _, d := range af.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, sp := range gd.Specs {
				ts := sp.(*ast.TypeSpec)
				it, ok := ts.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				ast.Inspect(it, func(n ast.Node) bool {
					sel, ok := n.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					x, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					path, isImport := imports[x.Name]
					if !isImport {
						return true
					}
					mod := r.ImportKey(path)
					if mod == "" {
						return true
					}
					if ident, ok := r.TypeOwner(mod, sel.Sel.Name); ok {
						if _, seen := out[ts.Name.Name]; !seen {
							out[ts.Name.Name] = Binding{Module: mod, Ident: ident}
						}
					}
					return true
				})
			}
		}
	}
	return out
}

func fileImports(r Resolver, af *ast.File) (map[string]string, []string) {
	imports := map[string]string{}
	var mods []string
	for _, im := range af.Imports {
		path, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			continue
		}
		local := defaultImportName(path)
		if im.Name != nil {
			local = im.Name.Name
		}
		imports[local] = path
		if k := r.ImportKey(path); k != "" && !slices.Contains(mods, k) {
			mods = append(mods, k)
		}
	}
	return imports, mods
}

var versionSegRe = regexp.MustCompile(`/v\d+[a-z0-9]*$`)

// defaultImportName is the package name an unaliased import binds: the last
// path segment after trailing major/API versions (armcompute/v6 → armcompute,
// compute/v1 → compute).
func defaultImportName(path string) string {
	for versionSegRe.MatchString(path) {
		path = versionSegRe.ReplaceAllString(path, "")
	}
	return path[strings.LastIndex(path, "/")+1:]
}

func recvType(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		return typeIdent(fd.Recv.List[0].Type)
	}
	return ""
}

func funcName(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		return typeIdent(fd.Recv.List[0].Type) + "." + fd.Name.Name
	}
	return fd.Name.Name
}

func typeIdent(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return typeIdent(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return typeIdent(t.X) + "." + t.Sel.Name
	case *ast.IndexExpr:
		return typeIdent(t.X)
	case *ast.IndexListExpr:
		return typeIdent(t.X)
	}
	return ""
}

// scanBody records type-constant references, label literals, package-local
// callees and type constants passed to them.
// storeWrites are the store methods that persist resource rows. Reaching one
// is the positive signal that a Type* identifier in the body names something
// the function stores. Relationship and hierarchy writes are deliberately
// absent: a resolver that pages a lister only to emit edges names its source
// types in a store.ResourceFilter and in case clauses, and counting those made
// microsoft.insights/diagnosticsettings claim 30 foreign types.
var storeWrites = map[string]bool{
	"UpsertResources":         true,
	"UpsertResource":          true,
	"InsertResourcesIfAbsent": true,
}

// storeResource is the row struct. Building one is the other positive signal:
// a scanner phase that appends to a batch its caller upserts never names a
// store method, and the batch helper never names the type.
const storeResource = "Resource"

func scanBody(f *fn, fd *ast.FuncDecl, fset *token.FileSet, consts map[string]string, varTypes map[string]map[string]bool, imports map[string]string, grammar, loose *regexp.Regexp) {
	params := paramNames(fd)
	concat := map[token.Pos]bool{} // literal operands of +: a prefix, not a label
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				concat[x.X.Pos()] = true
				concat[x.Y.Pos()] = true
			}
		case *ast.Ident:
			if v, ok := consts[x.Name]; ok {
				f.types[v] = true
			}
			for v := range varTypes[x.Name] { // a package-level table of types
				f.types[v] = true
			}
		case *ast.CompositeLit:
			// store.Resource specifically: another package's Resource type is
			// not a row (armfoo.Resource is an ARM envelope).
			if sel, ok := x.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == storeResource {
				if id, isIdent := sel.X.(*ast.Ident); isIdent && strings.HasSuffix(imports[id.Name], "/store") {
					f.stores = true
				}
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING && !concat[x.Pos()] {
				v, err := strconv.Unquote(x.Value)
				switch {
				case err != nil:
				case grammar.MatchString(v):
					f.labels[v] = append(f.labels[v], fset.Position(x.Pos()).Line)
				case loose != nil && loose.MatchString(v):
					f.near[v] = append(f.near[v], fset.Position(x.Pos()).Line)
				}
			}
		case *ast.CallExpr:
			scanCall(f, x, params, consts, imports)
		}
		return true
	})
}

// scanCall records one call site: the callee, whether it writes rows, and
// whether it is handed something this function produced or forwarded.
func scanCall(f *fn, x *ast.CallExpr, params map[string]bool, consts, imports map[string]string) {
	if sel, ok := x.Fun.(*ast.SelectorExpr); ok && storeWrites[sel.Sel.Name] {
		f.stores = true
	}
	callee := ""
	switch fun := x.Fun.(type) {
	case *ast.Ident:
		callee = fun.Name
	case *ast.IndexExpr: // generic helper instantiation
		if id, ok := fun.X.(*ast.Ident); ok {
			callee = id.Name
		}
	case *ast.SelectorExpr:
		if id, ok := fun.X.(*ast.Ident); ok {
			if _, isImport := imports[id.Name]; !isImport {
				callee = id.Name + "." + fun.Sel.Name // method on a local receiver
			}
		}
	}
	if callee == "" {
		return
	}
	f.callees[callee] = true
	if feedsLocal(x.Args, params, imports) {
		f.feeds[callee] = true
	}
	if forwardsParam(x.Args, params) {
		f.forwards[callee] = true
	}
	for _, arg := range x.Args {
		switch a := arg.(type) {
		case *ast.Ident:
			if v, ok := consts[a.Name]; ok {
				if f.outflow[callee] == nil {
					f.outflow[callee] = map[string]bool{}
				}
				f.outflow[callee][v] = true
			}
		case *ast.SelectorExpr: // forEachItem(ctx, n, items, s.scanDataset): a method value
			if id, isIdent := a.X.(*ast.Ident); isIdent {
				if _, isImport := imports[id.Name]; !isImport {
					f.callees[id.Name+"."+a.Sel.Name] = true
				}
			}
		}
	}
}

// paramNames is the function's own parameters and receiver: the values it was
// handed rather than produced.
func paramNames(fd *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	add := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, p := range fl.List {
			for _, n := range p.Names {
				out[n.Name] = true
			}
		}
	}
	add(fd.Recv)
	if fd.Type != nil {
		add(fd.Type.Params)
	}
	return out
}

// feedsLocal reports whether a call hands the callee something this function
// produced — a paged result, a batch, a client — rather than only the plumbing
// it was handed itself. A helper called with nothing but (ctx, st, scanID)
// cannot be storing rows from the caller's listing, so the types it names are
// not what the caller's anchored op returns.
func feedsLocal(args []ast.Expr, params map[string]bool, imports map[string]string) bool {
	var local func(e ast.Expr) bool
	local = func(e ast.Expr) bool {
		switch a := e.(type) {
		case *ast.Ident:
			_, isImport := imports[a.Name]
			return !isImport && !params[a.Name] && a.Name != "nil"
		case *ast.UnaryExpr: // &batch
			return local(a.X)
		case *ast.SelectorExpr: // ph.discoType, out.Items
			return local(a.X)
		case *ast.IndexExpr:
			return local(a.X)
		case *ast.CallExpr, *ast.CompositeLit, *ast.BasicLit:
			return true // a value built here, not one handed in
		}
		return false
	}
	for _, a := range args {
		if local(a) {
			return true
		}
	}
	return false
}

// forwardsParam reports whether a call hands the callee one of this
// function's own parameters. On its own that proves nothing; combined with
// this function having been fed, it is how a pass-through chain carries a
// listing four hops down to the helper that stores it.
func forwardsParam(args []ast.Expr, params map[string]bool) bool {
	for _, a := range args {
		if id, ok := a.(*ast.Ident); ok && params[id.Name] {
			return true
		}
	}
	return false
}

// linkFeeds closes the feeding relation: a function fed by a caller carries
// that listing on into the helpers it forwards its parameters to.
func linkFeeds(fns map[string]*fn, order []*fn) {
	for changed := true; changed; {
		changed = false
		for _, f := range order {
			for _, c := range sortedSet(f.callees) {
				if !f.feeds[c] && f.carries && f.forwards[c] {
					f.feeds[c], changed = true, true
				}
				if !f.feeds[c] {
					continue
				}
				if g := resolveCallee(fns, c); g != nil && !g.carries {
					g.carries, changed = true, true
				}
			}
		}
	}
}

// bindLocals maps locals assigned from SDK constructors (client, err :=
// armX.NewYClient(...); svc, err := api.NewService(...); c := cf.NewYClient())
// and parameters typed *mod.Ident, or as a package-local interface seam
// over one client, to their origin.
func bindLocals(fd *ast.FuncDecl, imports map[string]string, r Resolver, seams map[string]Binding) map[string]Binding {
	vars := map[string]Binding{}
	bind := func(name string, e ast.Expr) {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return
		}
		mod := ""
		if path, isImport := imports[x.Name]; isImport {
			mod = r.ImportKey(path)
		} else if b, isVar := vars[x.Name]; isVar {
			mod = b.Module // client factory
		}
		if mod == "" {
			return
		}
		if ident, ok := r.Constructor(mod, sel.Sel.Name); ok {
			vars[name] = Binding{Module: mod, Ident: ident}
		}
	}
	if fd.Type.Params != nil {
		for _, p := range fd.Type.Params.List {
			if id, ok := p.Type.(*ast.Ident); ok {
				if b, isSeam := seams[id.Name]; isSeam {
					for _, n := range p.Names {
						vars[n.Name] = b
					}
				}
				continue
			}
			sel, ok := unstar(p.Type).(*ast.SelectorExpr)
			if !ok {
				continue
			}
			x, ok := sel.X.(*ast.Ident)
			if !ok {
				continue
			}
			if path, isImport := imports[x.Name]; isImport {
				if mod := r.ImportKey(path); mod != "" {
					for _, n := range p.Names {
						vars[n.Name] = Binding{Module: mod, Ident: sel.Sel.Name}
					}
				}
			}
		}
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			if len(x.Rhs) == 1 && len(x.Lhs) >= 1 {
				if id, ok := x.Lhs[0].(*ast.Ident); ok {
					bind(id.Name, x.Rhs[0])
				}
			}
		case *ast.ValueSpec:
			if len(x.Values) == 1 && len(x.Names) >= 1 {
				bind(x.Names[0].Name, x.Values[0])
			}
		}
		return true
	})
	return vars
}

func unstar(e ast.Expr) ast.Expr {
	if s, ok := e.(*ast.StarExpr); ok {
		return s.X
	}
	return e
}

// linkFlows records, on each callee, which caller passes it which type
// constants. The flow stays attributed to its caller: a shared store helper
// fed TypeA by one scanner and TypeB by another must not make either
// scanner's SDK call look like it stores both.
func linkFlows(fns map[string]*fn, order []*fn) {
	for _, f := range order {
		for callee, types := range f.outflow {
			g := resolveCallee(fns, callee)
			if g == nil {
				continue
			}
			if g.inflow[f.name] == nil {
				g.inflow[f.name] = map[string]bool{}
			}
			for t := range types {
				g.inflow[f.name][t] = true
			}
		}
	}
}

// dropEmittedFromDerived keeps a derived pairing only for types no
// SDK-anchored function stores itself: a dispatcher referencing a type it
// also queries (Types: []string{TypeBackupVault}) does not re-pair it.
func dropEmittedFromDerived(ps []Pairing) []Pairing {
	emitted := map[string]bool{}
	for _, p := range ps {
		if p.Kind == "emits" {
			for _, t := range p.Types {
				emitted[t] = true
			}
		}
	}
	out := ps[:0]
	for _, p := range ps {
		if p.Kind == "derived" {
			var keep []string
			for _, t := range p.Types {
				if !emitted[t] {
					keep = append(keep, t)
				}
			}
			if len(keep) == 0 {
				continue
			}
			p.Types = keep
		}
		out = append(out, p)
	}
	return out
}

// relatedTypes keeps the orphan types whose service segment relates to the
// operation's service, or whose leaf matches the candidate key's last segment.
// A derived pairing is evidence by proximity — the rows come from this
// listing — and proximity across services is not evidence at all.
func relatedTypes(key string, op sdkinv.Operation, orphan []string) []string {
	svc := normIdent(op.Service)
	leaf := key
	if i := strings.LastIndex(leaf, "/"); i >= 0 {
		leaf = leaf[i+1:]
	}
	leaf = sdkinv.Ident(leaf)
	var out []string
	for _, t := range orphan {
		parts := strings.SplitN(t, ":", 3)
		if len(parts) != 3 {
			continue
		}
		ts := normIdent(parts[1])
		segs := strings.Split(parts[2], ":")
		tl := sdkinv.Ident(segs[len(segs)-1])
		if ts == svc || strings.HasPrefix(ts, svc) || strings.HasPrefix(svc, ts) || tl == leaf {
			out = append(out, t)
		}
	}
	return out
}

// normIdent reduces a service name to the form the two sides compare in:
// "microsoft.resources" and "resources", "cloudkms" and "kms".
func normIdent(s string) string {
	s = strings.ToLower(s)
	s = strings.NewReplacer(".", "", "-", "", "_", "").Replace(s)
	if _, rest, found := strings.Cut(s, "/"); found {
		s = rest
	}
	return s
}

// orphanTypes lists the function's reachable types that no callee with an
// SDK call of its own (or promoted from a listing helper) reaches itself.
func (f *fn) orphanTypes(fns map[string]*fn) []string {
	covered := map[string]bool{}
	walkCallees(fns, f, func(cur *fn) {
		if cur == f || len(cur.anchored)+len(cur.other)+len(cur.missing)+len(cur.typeless) == 0 {
			return
		}
		for _, t := range cur.atypes {
			covered[t] = true
		}
	})
	// The derived path is evidence by proximity, so it reads the unpruned set:
	// a synthesiser called with nothing but (st, nil) builds its rows from a
	// sibling's listing, which is exactly what "derived" means. The emits path
	// uses the fed set instead — see walkFedCallees.
	var out []string
	for _, t := range f.atypes {
		if !covered[t] {
			out = append(out, t)
		}
	}
	return out
}

func sortedKeys(m map[string]opRef) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolveCallee finds the package-local function a callee key names. A
// method call on a local (s.scanTables) resolves to the one method of that
// name in the package; ambiguous names resolve to nothing.
func resolveCallee(fns map[string]*fn, c string) *fn {
	if g := fns[c]; g != nil {
		return g
	}
	if _, method, ok := strings.Cut(c, "."); ok {
		return fns[methodKey(method)]
	}
	return nil
}

// linkCallers records each function's direct callers.
func linkCallers(fns map[string]*fn, order []*fn) {
	for _, f := range order {
		for c := range f.callees {
			if g := resolveCallee(fns, c); g != nil && g != f {
				g.callers = append(g.callers, f)
			}
		}
	}
}

// walkFedCallees is walkCallees restricted to calls that hand the callee
// something the caller produced. A dispatcher that anchors a listing and then
// calls an unrelated helper with nothing but (ctx, st) is not storing that
// helper's types from its own listing, and crediting them made the helper's
// rows read as emitted under whichever op the dispatcher happened to call.
func walkFedCallees(fns map[string]*fn, f *fn, visit func(*fn)) {
	seen := map[string]bool{f.name: true}
	frontier := []*fn{f}
	for depth := 0; len(frontier) > 0; depth++ {
		var next []*fn
		for _, cur := range frontier {
			visit(cur)
			if depth >= calleeDepth && len(cur.anchors) > 0 {
				continue
			}
			for _, c := range sortedSet(cur.callees) {
				if !cur.feeds[c] {
					continue
				}
				if g := resolveCallee(fns, c); g != nil && !seen[g.name] {
					seen[g.name] = true
					next = append(next, g)
				}
			}
		}
		frontier = next
	}
}

// walkCallees visits f and its package-local callees up to calleeDepth.
// Callees are visited in name order: the visit order decides which of two
// callees anchoring the same candidate is reported, and a map range would
// make that choice differ between runs of the same binary.
func walkCallees(fns map[string]*fn, f *fn, visit func(*fn)) {
	seen := map[string]bool{f.name: true}
	frontier := []*fn{f}
	for depth := 0; len(frontier) > 0; depth++ {
		var next []*fn
		for _, cur := range frontier {
			visit(cur)
			// Past the cap the walk continues only through helpers with no
			// SDK call of their own. The cap is what holds over-attribution
			// down — dropping it would union every transitively reachable
			// type into every anchored op — but a store four plain hops down
			// is a correct scanner, and cutting there reported it as
			// unexplained and failed both gates.
			if depth >= calleeDepth && len(cur.anchors) > 0 {
				continue
			}
			for _, c := range sortedSet(cur.callees) {
				if g := resolveCallee(fns, c); g != nil && !seen[g.name] {
					seen[g.name] = true
					next = append(next, g)
				}
			}
		}
		frontier = next
	}
}

// sortedLines orders label literals so the diagnostics are reproducible.
func sortedLines(m map[string][]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedSet orders a call-site set so every walk over it is reproducible.
func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// betterAnchor reports whether a should replace b as the op reported for a
// candidate two functions both anchor. A listing beats a single-item read
// (scanGuardDutyDetectors anchors both ListDetectors and GetDetector), and
// the smaller label breaks the remaining ties so the choice is total.
func betterAnchor(a, b sdkinv.Operation) bool {
	if a.IsList != b.IsList {
		return a.IsList
	}
	return a.Label < b.Label
}

// methodKey is the fns key under which a uniquely named method is also
// registered, so "s.scanTables" finds "bigQueryScan.scanTables".
func methodKey(method string) string { return "*." + method }

// markStoring decides, per function, whether the Type* identifiers in its body
// name rows the scan stores. The store call is rarely in the same body: a
// scanner hands a batch to upsertWithProjClosure one hop down, and a phase
// table (wafPhases) names the type and the SDK op while its storing driver
// sits one hop up and never sees the constant. So the signal travels both ways
// — down through callees, then up the caller chain to a fixpoint — and only a
// function no storing path reaches at all loses its types. That leaves the
// resolvers, which name their source types in a store.ResourceFilter and write
// edges alone; counting those made microsoft.insights/diagnosticsettings claim
// 30 foreign types.
func markStoring(fns map[string]*fn, order []*fn) {
	for _, f := range order {
		walkCallees(fns, f, func(cur *fn) {
			if cur.stores {
				f.storesReach = true
			}
		})
	}
	for changed := true; changed; {
		changed = false
		for _, f := range order {
			if f.storesReach {
				continue
			}
			for _, c := range f.callers {
				if c.storesReach {
					f.storesReach, changed = true, true
					break
				}
			}
		}
	}
}

// reachableTypes is a function's own types plus those of package-local
// callees, so a lister whose page handler is a named helper still pairs
// with the types that helper stores.
func reachableTypes(fns map[string]*fn, f *fn, ownOnly bool, walk func(map[string]*fn, *fn, func(*fn))) []string {
	var visited []*fn
	inWalk := map[string]bool{}
	walk(fns, f, func(cur *fn) {
		visited = append(visited, cur)
		inWalk[cur.name] = true
	})
	types := map[string]bool{}
	for _, cur := range visited {
		for t := range cur.types {
			types[t] = true
		}
		// Types passed into a helper count for the helper itself (it stores
		// whatever any caller hands it) and for callers inside this walk.
		for caller, ts := range cur.inflow {
			if !inWalk[caller] && (ownOnly || cur != f) {
				continue
			}
			for t := range ts {
				types[t] = true
			}
		}
	}
	out := make([]string, 0, len(types))
	for t := range types {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// reachableAnchors is the candidates anchored by a function or its callees,
// and those a direct callee anchors while storing nothing itself (a pager
// builder returning rows to this function).
func reachableAnchors(fns map[string]*fn, f *fn) (map[string]opRef, map[string]bool) {
	out := map[string]opRef{}
	walkCallees(fns, f, func(cur *fn) {
		for _, k := range sortedKeys(cur.anchored) {
			v := cur.anchored[k]
			if have, ok := out[k]; ok && !betterAnchor(v.op, have.op) {
				continue
			}
			out[k] = v
		}
	})
	typeless := map[string]bool{}
	for _, c := range sortedSet(f.callees) {
		if g := resolveCallee(fns, c); g != nil && g != f && len(g.rtypes) == 0 {
			for k := range g.anchored {
				typeless[k] = true
			}
		}
	}
	return out, typeless
}

// SDKFiles reports, per constant name, whether some non-init function or
// package-level var that references it lives in a file importing an SDK package.
func SDKFiles(dir string, r Resolver) (map[string]bool, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	out := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, perr := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if perr != nil {
			return nil, perr
		}
		_, mods := fileImports(r, af)
		if len(mods) == 0 {
			continue
		}
		mark := func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				out[id.Name] = true
			}
			return true
		}
		for _, d := range af.Decls {
			switch x := d.(type) {
			case *ast.FuncDecl:
				if x.Body != nil && x.Name.Name != "init" {
					ast.Inspect(x.Body, mark)
				}
			case *ast.GenDecl: // a package-level table of types
				if x.Tok == token.VAR {
					ast.Inspect(x, mark)
				}
			}
		}
	}
	return out, nil
}

// Line is the source line of n within the function's file.
func (f *Func) Line(n ast.Node) int { return f.fset.Position(n.Pos()).Line }

// skewed reports whether a label's operation is one of the function's SDK
// calls that the pinned SDK no longer ships (armcompute v6 CloudServices vs
// HEAD).
func skewed(op string, missing []string) bool {
	want := sdkinv.Canon(op)
	return slices.ContainsFunc(missing, func(m string) bool { return sdkinv.Canon(m) == want })
}

// moduleAbsent reports whether the label's module prefix is one the file
// imports but the pinned SDK does not ship at all (armappplatform).
func moduleAbsent(lit string, mods []string, modules map[string]bool) bool {
	prefix, _, _ := strings.Cut(lit, ":")
	for _, m := range mods {
		if !modules[m] && (m == prefix || sdkinv.Canon(m) == sdkinv.Canon(prefix)) {
			return true
		}
	}
	return false
}

func sortedOps(m map[string]sdkinv.Operation) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
