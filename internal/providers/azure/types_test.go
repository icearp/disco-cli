package azure

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEveryTypeConstantIsUsed guards against orphan disco-type constants
// in types.go — a Type* identifier declared but referenced nowhere else in
// the package. Without this guard, retired services leave dead constants
// behind that silently expand the package vocabulary.
//
// "Used" means the identifier appears as an ast.Ident in any other .go
// file in the package — emits decl, scanner upsert call, resolver edge
// emit, test fixture, redact rule, etc. all count.
func TestEveryTypeConstantIsUsed(t *testing.T) {
	declared := parseTypeConstants(t, "types.go")
	used := collectIdentRefs(t, ".", "types.go")

	var orphans []string
	for name, val := range declared {
		if !used[name] {
			orphans = append(orphans, name+" = "+strconv.Quote(val))
		}
	}
	if len(orphans) > 0 {
		t.Errorf("orphan disco-type constants in types.go (declared but referenced nowhere else in the package):\n  %s",
			strings.Join(orphans, "\n  "))
	}
}

// parseTypeConstants walks the AST of path and returns every top-level
// `Type* = "..."` constant (name → string value).
func parseTypeConstants(t *testing.T, path string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			return true
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != len(vs.Names) {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Type") {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				out[name.Name] = val
			}
		}
		return true
	})
	return out
}

// collectIdentRefs parses every .go file in dir except skip and returns
// the set of Type*-prefixed identifier names referenced in any expression
// (not the declaration site itself).
func collectIdentRefs(t *testing.T, dir, skip string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	refs := map[string]bool{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || name == skip {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if strings.HasPrefix(id.Name, "Type") {
				refs[id.Name] = true
			}
			return true
		})
	}
	return refs
}
