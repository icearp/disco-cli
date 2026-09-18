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

// Diagnostic is a pairing problem the strict test fails on.
type Diagnostic struct {
	Kind    string `json:"kind"` // label-no-op | label-no-anchor | op-not-in-inventory | unresolved-receiver
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
}

// Func is the per-function view a Resolver inspects.
type Func struct {
	provider string
	fset     *token.FileSet
	Decl     *ast.FuncDecl
	Imports  map[string]string // local name -> import path
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
	Anchors(f *Func) ([]Anchor, []Diagnostic)
}

var resolvers = map[string]Resolver{}

// Register adds a provider resolver; called from init.
func Register(r Resolver) { resolvers[r.Name()] = r }

// Get returns the resolver for a provider.
func Get(name string) (Resolver, bool) {
	r, ok := resolvers[name]
	return r, ok
}

const calleeDepth = 3

type fn struct {
	name    string
	file    string
	line    int
	decl    *ast.FuncDecl
	types   map[string]bool
	outflow map[string]map[string]bool // callee -> type constants passed to it as arguments
	inflow  map[string]map[string]bool // caller -> type constants it passes in
	labels  map[string][]int           // label literal -> lines
	callees map[string]bool
	anchors []Anchor
	diags   []Diagnostic
	mods    []string // SDK module keys the file imports
	callers []*fn

	rtypes   []string                    // own types plus those of callees
	anchored map[string]opRef            // candidate key -> op, from own anchors
	other    map[string]sdkinv.Operation // non-candidate ops, by label
	missing  []string                    // bound SDK calls the pinned SDK no longer has
	reach    map[string]opRef            // anchored by this function or its callees
	typeless map[string]bool             // reach keys a direct callee anchors without storing
}

// Walk parses every non-test .go file in dir and pairs it against u.
func Walk(dir string, u *sdkinv.Universe) (*Result, error) {
	r, ok := resolvers[u.Provider]
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
	idx := indexUniverse(r, u)
	consts := collectConsts(parsed, u.Provider)
	varTypes := collectVarTypes(parsed, consts)
	fields := collectFields(parsed, r)
	seams := collectSeams(parsed, r)
	res := &Result{Provider: u.Provider, Consts: consts}
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
				labels: map[string][]int{}, callees: map[string]bool{}, mods: mods,
			}
			scanBody(f, fd, fset, consts, varTypes, imports, r.LabelGrammar())
			fv := &Func{
				provider: u.Provider, fset: fset, Decl: fd, Imports: imports, Vars: bindLocals(fd, imports, r, seams), Fields: fields[recvType(fd)], SDKMods: mods,
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
	for _, f := range order {
		f.rtypes = reachableTypes(fns, f)
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
	for k, v := range f.reach {
		if f.typeless[k] && len(types) > 0 {
			anchored[k] = v
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
	if len(anchored) == 0 && len(f.reach) > 0 {
		if orphan := f.orphanTypes(fns); len(orphan) > 0 {
			for _, k := range sortedKeys(f.reach) {
				op := f.reach[k].op
				res.Pairings = append(res.Pairings, Pairing{
					Provider: provider, Service: op.Service, Key: k, Op: op.Name, Label: op.Label,
					Types: orphan, File: f.file, Line: f.line, Func: f.name, Kind: "derived",
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
	labelOps := map[string][]string{} // candidate key -> label literals in this function
	labelOnly := map[string]bool{}
	for lit, lines := range f.labels {
		ref, ok := idx.resolveLabel(lit, f.mods, reach)
		if !ok {
			switch {
			case idx.known(lit, f.mods):
			case skewed(lit, missing):
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
		kind := "emits"
		switch {
		case labelOnly[key]:
			kind = "label"
		case len(types) == 0:
			kind = "sidecar"
		}
		labels := labelOps[key]
		sort.Strings(labels)
		res.Pairings = append(res.Pairings, Pairing{
			Provider: provider, Service: op.Service, Key: key, Op: op.Name, Label: op.Label,
			Types: types, Labels: labels, File: f.file, Line: f.line, Func: f.name, Kind: kind,
		})
	}
}

// Unpaired returns every string constant whose value looks like a disco type
// for this provider and that no pairing carries, with a reason: "non-sdk"
// when every file referencing it imports no SDK package, else "unexplained".
func (res *Result) Unpaired(consts map[string]string, sdkFiles map[string]bool) map[string]string {
	paired := map[string]bool{}
	viaOther := map[string]string{}
	for _, p := range res.Pairings {
		for _, t := range p.Types {
			switch p.Kind {
			case "other":
				viaOther[t] = "other-op:" + p.Label
			case "skew":
				viaOther[t] = "sdk-skew:" + p.Label
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
	byKey   map[string]opRef   // module\x00op
	byLabel map[string][]opRef // candidate ops by every alias form (several when the alias is ambiguous)
	other   map[string]sdkinv.Operation
	clients map[string][]string // module\x00op -> client idents
	modules map[string]bool
}

func indexUniverse(r Resolver, u *sdkinv.Universe) *index {
	idx := &index{byKey: map[string]opRef{}, byLabel: map[string][]opRef{}, other: map[string]sdkinv.Operation{}, clients: map[string][]string{}, modules: map[string]bool{}}
	for _, c := range u.Candidates {
		for _, op := range c.Ops {
			mod, name := r.OpKey(op)
			idx.modules[mod] = true
			idx.byKey[mod+"\x00"+name] = opRef{op, c.Key}
			for _, l := range r.LabelAliases(c, op) {
				idx.byLabel[l] = append(idx.byLabel[l], opRef{op, c.Key})
			}
			if client, method, ok := strings.Cut(name, "."); ok {
				idx.clients[mod+"\x00"+method] = append(idx.clients[mod+"\x00"+method], client)
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
func scanBody(f *fn, fd *ast.FuncDecl, fset *token.FileSet, consts map[string]string, varTypes map[string]map[string]bool, imports map[string]string, grammar *regexp.Regexp) {
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
		case *ast.BasicLit:
			if x.Kind == token.STRING && !concat[x.Pos()] {
				if v, err := strconv.Unquote(x.Value); err == nil && grammar.MatchString(v) {
					f.labels[v] = append(f.labels[v], fset.Position(x.Pos()).Line)
				}
			}
		case *ast.CallExpr:
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
				return true
			}
			f.callees[callee] = true
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
					if id, ok := a.X.(*ast.Ident); ok {
						if _, isImport := imports[id.Name]; !isImport {
							f.callees[id.Name+"."+a.Sel.Name] = true
						}
					}
				}
			}
		}
		return true
	})
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

// orphanTypes lists the function's reachable types that no callee with an
// SDK call of its own (or promoted from a listing helper) reaches itself.
func (f *fn) orphanTypes(fns map[string]*fn) []string {
	covered := map[string]bool{}
	walkCallees(fns, f, func(cur *fn) {
		if cur == f || len(cur.anchored)+len(cur.other)+len(cur.missing)+len(cur.typeless) == 0 {
			return
		}
		for _, t := range cur.rtypes {
			covered[t] = true
		}
	})
	var out []string
	for _, t := range f.rtypes {
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

// walkCallees visits f and its package-local callees up to calleeDepth.
// Callees are visited in name order: the visit order decides which of two
// callees anchoring the same candidate is reported, and a map range would
// make that choice differ between runs of the same binary.
func walkCallees(fns map[string]*fn, f *fn, visit func(*fn)) {
	seen := map[string]bool{f.name: true}
	frontier := []*fn{f}
	for depth := 0; depth <= calleeDepth && len(frontier) > 0; depth++ {
		var next []*fn
		for _, cur := range frontier {
			visit(cur)
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

// reachableTypes is a function's own types plus those of package-local
// callees, so a lister whose page handler is a named helper still pairs
// with the types that helper stores.
func reachableTypes(fns map[string]*fn, f *fn) []string {
	var visited []*fn
	inWalk := map[string]bool{}
	walkCallees(fns, f, func(cur *fn) {
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
			if cur != f && !inWalk[caller] {
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

func line(f *Func, n ast.Node) int { return f.fset.Position(n.Pos()).Line }

// skewed reports whether a label names one of the function's SDK calls that
// the pinned SDK no longer ships (armcompute v6 CloudServices vs HEAD).
func skewed(lit string, missing []string) bool {
	_, name, _ := strings.Cut(lit, ":")
	want := sdkinv.Canon(strings.ReplaceAll(name, "Client", ""))
	for _, m := range missing {
		if sdkinv.Canon(strings.ReplaceAll(m, "Client", "")) == want {
			return true
		}
	}
	return false
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
