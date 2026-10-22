package aws

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestEveryResolverIsRegistered fails when a function with the resolver
// signature is neither registered nor reached from a registered resolver.
// Tests call resolvers directly, so a dropped registerResolver call otherwise
// leaves the package green while production never runs the resolver.
func TestEveryResolverIsRegistered(t *testing.T) {
	candidates := parseResolverShapedFuncs(t, ".")
	if len(candidates) == 0 {
		t.Fatal("parsed no resolver-shaped functions; the signature match is broken")
	}

	reached := map[string]bool{}
	var queue []string
	for _, r := range registeredResolvers {
		if !reached[r.name] {
			reached[r.name] = true
			queue = append(queue, r.name)
		}
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		body, ok := candidates[name]
		if !ok {
			continue
		}
		ast.Inspect(body, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if ok && candidates[id.Name] != nil && !reached[id.Name] {
				reached[id.Name] = true
				queue = append(queue, id.Name)
			}
			return true
		})
	}

	var orphans []string
	for name := range candidates {
		if !reached[name] {
			orphans = append(orphans, name)
		}
	}
	slices.Sort(orphans)
	if len(orphans) > 0 {
		t.Errorf("resolver-shaped functions never registered with registerResolver (or reached from one that is), so no scan runs them:\n  %s",
			strings.Join(orphans, "\n  "))
	}
}

// parseResolverShapedFuncs returns every top-level non-test function in dir
// whose signature is func(*account, *store.Store) error, keyed by name.
func parseResolverShapedFuncs(t *testing.T, dir string) map[string]*ast.BlockStmt {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.BlockStmt{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, dir+"/"+name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if ok && fd.Recv == nil && fd.Body != nil && isResolverSignature(fd.Type) {
				out[fd.Name.Name] = fd.Body
			}
		}
	}
	return out
}

func isResolverSignature(ft *ast.FuncType) bool {
	var params []ast.Expr
	for _, p := range ft.Params.List {
		for range max(len(p.Names), 1) {
			params = append(params, p.Type)
		}
	}
	if len(params) != 2 || ft.Results == nil || len(ft.Results.List) != 1 {
		return false
	}
	return isPointerTo(params[0], "", "account") &&
		isPointerTo(params[1], "store", "Store") &&
		isIdent(ft.Results.List[0].Type, "error")
}

// isPointerTo reports whether e is *name, or *pkg.name when pkg is non-empty.
func isPointerTo(e ast.Expr, pkg, name string) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	if pkg == "" {
		return isIdent(star.X, name)
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && isIdent(sel.X, pkg) && sel.Sel.Name == name
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}
