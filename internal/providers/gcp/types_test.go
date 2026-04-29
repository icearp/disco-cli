package gcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestKnownTypes_NoOmissions parses types.go via go/ast, extracts every
// `Type[A-Z]\w* = "..."` const, and asserts each value appears in
// KnownTypes(). Closes the "no test catches omission" gap noted in
// internal/providers/CLAUDE.md "New Type* constant → append to KnownTypes()".
//
// AST parse over reflection because Go const blocks aren't introspectable at
// runtime — package-level const values are only visible to source-level tools.
func TestKnownTypes_NoOmissions(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "types.go", nil, 0)
	if err != nil {
		t.Fatalf("parse types.go: %v", err)
	}

	var declared []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Type") || len(name.Name) < 5 || !isUpper(name.Name[4]) {
					continue
				}
				if i >= len(vs.Values) {
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
				declared = append(declared, val)
			}
		}
	}

	if len(declared) == 0 {
		t.Fatal("AST scan found no Type* consts — parser broken or types.go renamed")
	}

	known := KnownTypes()
	knownSet := make(map[string]bool, len(known))
	for _, k := range known {
		knownSet[k] = true
	}

	for _, d := range declared {
		if !knownSet[d] {
			t.Errorf("Type* const %q declared in types.go but missing from KnownTypes()", d)
		}
	}
	for _, k := range known {
		if !slices.Contains(declared, k) {
			t.Errorf("KnownTypes() lists %q but no matching Type* const found in types.go", k)
		}
	}
}

// TestKnownTypes_NoDuplicates guards against copy-paste errors that would
// double-count a type in coverage matrices.
func TestKnownTypes_NoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range KnownTypes() {
		if seen[k] {
			t.Errorf("duplicate entry in KnownTypes(): %q", k)
		}
		seen[k] = true
	}
}

func isUpper(b byte) bool { return b >= 'A' && b <= 'Z' }
