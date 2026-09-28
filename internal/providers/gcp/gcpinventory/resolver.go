package gcpinventory

import (
	"fmt"
	"go/ast"
	"regexp"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv/pairing"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

const gcpPrefix = "google.golang.org/api/"

var gcpLabelRe = regexp.MustCompile(`^[a-z0-9]+:[a-z][A-Za-z0-9]*(\.[a-z][A-Za-z0-9]*)+$`)

type gcpResolver struct{}

func init() { pairing.Register(gcpResolver{}) }

func (gcpResolver) Name() string                 { return "gcp" }
func (gcpResolver) LabelGrammar() *regexp.Regexp { return gcpLabelRe }

// ImportKey: the Discovery API name, the first segment under the module. A
// helper package (option, googleapi) keys too; it simply has no ops.
func (gcpResolver) ImportKey(path string) string {
	rest, ok := strings.CutPrefix(path, gcpPrefix)
	if !ok {
		return ""
	}
	api, _, _ := strings.Cut(rest, "/")
	return api
}

// OpKey: Module is google.golang.org/api@<ver>/<api>/<...>/<ver>. The name is
// canonical per dotted segment, as Anchors spells a Go selector chain: the
// generator turns Discovery's iap_tunnel into the Go field IapTunnel.
func (gcpResolver) OpKey(op sdkinv.Operation) (string, string) {
	_, rest, _ := strings.Cut(op.Module, "@")
	_, rest, _ = strings.Cut(rest, "/")
	api, _, _ := strings.Cut(rest, "/")
	return api, canonPath(strings.Split(op.Name, "."))
}

func canonPath(segs []string) string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = sdkinv.Canon(s)
	}
	return strings.Join(out, ".")
}

// LabelAliases: scanners label by the scope-stripped path
// ("run:jobs.executions.list" for projects.locations.jobs.executions.list),
// for Other ops too ("monitoring:groups.members.list").
func (gcpResolver) LabelAliases(c sdkinv.Candidate, op sdkinv.Operation) []string {
	method := op.Name[strings.LastIndex(op.Name, ".")+1:]
	nodes := strings.Split(strings.TrimSuffix(op.Name, "."+method), ".")
	if c.Key == "" {
		// An op in Other has no key; drop the declared scope nodes instead.
		kept := nodes[:0:0]
		for i, n := range nodes {
			if lower := strings.ToLower(n); i == len(nodes)-1 || cloudRoots[lower] == "" && !placements[lower] {
				kept = append(kept, n)
			}
		}
		nodes = kept
	} else {
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
// (s.svc.Instances → the field svc, which then leaves the chain). Fields are
// compared canonically, as the chain is.
func receiverBinding(f *pairing.Func, root ast.Expr, fields *[]string) (pairing.Binding, bool) {
	if id, ok := root.(*ast.Ident); ok {
		if b, bound := f.Vars[id.Name]; bound {
			return b, true
		}
	}
	if len(*fields) > 0 {
		for name, b := range f.Fields {
			if sdkinv.Canon(name) == (*fields)[0] {
				*fields = (*fields)[1:]
				return b, true
			}
		}
	}
	return pairing.Binding{}, false
}

// TypeOwner: a Discovery method is reached through the service's resource
// chain, which an interface seam over one call type does not carry.
func (gcpResolver) TypeOwner(_, _ string) (string, bool) { return "", false }

// LabelOp: "<api>:<method path>", canonical per segment like OpKey.
func (gcpResolver) LabelOp(lit string) string {
	_, op, _ := strings.Cut(lit, ":")
	return canonPath(strings.Split(op, "."))
}

func (gcpResolver) Constructor(_, fn string) (string, bool) {
	if fn == "NewService" || fn == "New" {
		return "Service", true
	}
	return "", false
}

// Anchors: on a bound *Service every call down the resource chain is a
// Discovery method, so it anchors even when the pinned SDK lacks it (the
// sdk-skew diagnostic). On an unbound root, a chain naming a candidate op of
// exactly one imported API anchors there. The selector chain is the method
// path, canonical per segment.
func (gcpResolver) Anchors(f *pairing.Func) ([]pairing.Anchor, []pairing.Diagnostic) {
	var out []pairing.Anchor
	var diags []pairing.Diagnostic
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
			fields = append([]string{sdkinv.Canon(s.Sel.Name)}, fields...)
			x = s.X
		}
		if len(fields) == 0 {
			return true
		}
		method := sdkinv.Canon(sel.Sel.Name)
		if b, bound := receiverBinding(f, x, &fields); bound {
			name := strings.Join(fields, ".") + "." + method
			// The generator names every resource-chain type *Service, so a
			// chain from one is a Discovery method even when the pin lacks it
			// (sdk-skew). Other bound types (a response page, googleapi.Error)
			// anchor only on a method the universe knows.
			if len(fields) > 0 && (strings.HasSuffix(b.Ident, "Service") || f.Ops(b.Module, name) || f.Other(b.Module, name)) {
				out = append(out, pairing.Anchor{Module: b.Module, Op: name, Line: f.Line(call)})
			}
			return true
		}
		name := strings.Join(fields, ".") + "." + method
		var mods []string
		for _, mod := range f.SDKMods {
			if f.Ops(mod, name) {
				mods = append(mods, mod)
			}
		}
		switch len(mods) {
		case 1:
			out = append(out, pairing.Anchor{Module: mods[0], Op: name, Line: f.Line(call)})
		case 0:
			// Not a Discovery lister the universe knows (a store or helper
			// method): silent — labels cross-check the real ones.
		default:
			diags = append(diags, pairing.Diagnostic{
				Kind: "unresolved-receiver", Line: f.Line(call),
				Message: fmt.Sprintf("%s: service receiver not bound and %d imported APIs have it", name, len(mods)),
			})
		}
		return true
	})
	return out, diags
}
