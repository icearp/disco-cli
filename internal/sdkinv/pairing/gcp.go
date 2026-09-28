package pairing

import (
	"fmt"
	"go/ast"
	"regexp"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

const gcpPrefix = "google.golang.org/api/"

var (
	gcpLabelRe   = regexp.MustCompile(`^[a-z0-9]+:[a-z][A-Za-z0-9]*(\.[a-z][A-Za-z0-9]*)+$`)
	gcpListerSet = map[string]bool{"List": true, "AggregatedList": true}
)

type gcpResolver struct{}

func init() { Register(gcpResolver{}) }

func (gcpResolver) Name() string                 { return "gcp" }
func (gcpResolver) LabelGrammar() *regexp.Regexp { return gcpLabelRe }

// ImportKey: the Discovery API name, the first segment under the module.
func (gcpResolver) ImportKey(path string) string {
	rest, ok := strings.CutPrefix(path, gcpPrefix)
	if !ok {
		return ""
	}
	api, _, _ := strings.Cut(rest, "/")
	switch api {
	case "option", "googleapi", "transport", "iterator", "internal", "idtoken", "impersonate":
		return ""
	}
	return api
}

// OpKey: Module is google.golang.org/api@<ver>/<api>/<...>/<ver>.
func (gcpResolver) OpKey(op sdkinv.Operation) (string, string) {
	_, rest, _ := strings.Cut(op.Module, "@")
	_, rest, _ = strings.Cut(rest, "/")
	api, _, _ := strings.Cut(rest, "/")
	return api, op.Name
}

// LabelAliases: scanners label by the scope-stripped path
// ("run:jobs.executions.list" for projects.locations.jobs.executions.list).
func (gcpResolver) LabelAliases(c sdkinv.Candidate, op sdkinv.Operation) []string {
	method := op.Name[strings.LastIndex(op.Name, ".")+1:]
	nodes := strings.Split(strings.TrimSuffix(op.Name, "."+method), ".")
	if c.Key != "" {
		// The candidate key is the method path without scope nodes, lower-cased;
		// keep the method path's own spelling of the nodes it retains.
		keep := map[string]bool{}
		for _, k := range strings.Split(strings.TrimPrefix(c.Key, c.Service+"/"), "/") {
			keep[k] = true
		}
		kept := nodes[:0:0]
		for _, n := range nodes {
			if keep[strings.ToLower(n)] {
				kept = append(kept, n)
			}
		}
		// An aggregatedList registered on a sibling collection (the Compute
		// regional twins) shares no node with this key, and filtering then
		// leaves nothing to build an alias from.
		if len(kept) > 0 {
			nodes = kept
		}
	}
	path := strings.Join(nodes, ".")
	return []string{op.Label, op.Service + ":" + path + "." + method, op.Service + ":" + nodes[len(nodes)-1] + "." + method}
}

// receiverBinding resolves the chain root: a bound local, or a struct field
// (s.svc.Instances → the field svc, which then leaves the chain).
func receiverBinding(f *Func, root ast.Expr, fields *[]string) (Binding, bool) {
	if id, ok := root.(*ast.Ident); ok {
		if b, bound := f.Vars[id.Name]; bound {
			return b, true
		}
	}
	if len(*fields) > 0 {
		for name, b := range f.Fields {
			if sdkinv.LowerFirst(name) == (*fields)[0] {
				*fields = (*fields)[1:]
				return b, true
			}
		}
	}
	return Binding{}, false
}

// TypeOwner: a Discovery method is reached through the service's resource
// chain, which an interface seam over one call type does not carry.
func (gcpResolver) TypeOwner(_, _ string) (string, bool) { return "", false }

// LabelOp: "<api>:<method path>".
func (gcpResolver) LabelOp(lit string) string {
	_, op, _ := strings.Cut(lit, ":")
	return op
}

func (gcpResolver) Constructor(_, fn string) (string, bool) {
	if fn == "NewService" || fn == "New" {
		return "Service", true
	}
	return "", false
}

// Anchors: svc.A.B.List( / .AggregatedList( and, on a bound service, any
// other Discovery method (Get, GetIamPolicy, Search) — the selector chain is
// the method path with each field lower-cased.
func (gcpResolver) Anchors(f *Func) ([]Anchor, []Diagnostic) {
	var out []Anchor
	var diags []Diagnostic
	ast.Inspect(f.Decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		var fields []string
		x := sel.X
		for {
			s, ok := x.(*ast.SelectorExpr)
			if !ok {
				break
			}
			fields = append([]string{sdkinv.LowerFirst(s.Sel.Name)}, fields...)
			x = s.X
		}
		if len(fields) == 0 {
			return true
		}
		name := strings.Join(fields, ".") + "." + sdkinv.LowerFirst(sel.Sel.Name)
		if b, bound := receiverBinding(f, x, &fields); bound {
			if len(fields) == 0 {
				return true
			}
			name = strings.Join(fields, ".") + "." + sdkinv.LowerFirst(sel.Sel.Name)
			if gcpListerSet[sel.Sel.Name] || f.Ops(b.Module, name) || f.Other(b.Module, name) {
				out = append(out, Anchor{Module: b.Module, Op: name, Line: f.Line(call)})
			}
			return true
		}
		if !gcpListerSet[sel.Sel.Name] {
			return true
		}
		var mods []string
		for _, mod := range f.SDKMods {
			if f.Ops(mod, name) {
				mods = append(mods, mod)
			}
		}
		switch len(mods) {
		case 1:
			out = append(out, Anchor{Module: mods[0], Op: name, Line: f.Line(call)})
		case 0:
			// Not a Discovery lister the universe knows (a store or helper
			// method): silent — labels cross-check the real ones.
		default:
			diags = append(diags, Diagnostic{
				Kind: "unresolved-receiver", Line: f.Line(call),
				Message: fmt.Sprintf("%s: service receiver not bound and %d imported APIs have it", name, len(mods)),
			})
		}
		return true
	})
	return out, diags
}
