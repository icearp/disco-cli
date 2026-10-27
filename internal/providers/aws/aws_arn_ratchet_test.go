package aws

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// isCommercialARNLiteral reports whether a string literal's value hard-codes
// the aws partition: it contains "arn:aws:" or is exactly "arn:aws" (the bare
// prefix a HasPrefix matcher would test).
func isCommercialARNLiteral(v string) bool {
	return v == "arn:aws" || strings.Contains(v, "arn:aws:")
}

// TestNoCommercialARNLiterals fails on every string literal in the
// package's production files that hard-codes the aws partition into an ARN.
// Such a literal builds or matches the commercial form only, so in aws-cn or
// aws-us-gov it names a resource that does not exist. Build ARNs with arnFor or
// an aws_arn.go helper and acct.partition(); match them with parseARN or
// isARNOf. It reads string literals from the AST, so comments never count, and
// it sees single literals only: a commercial ARN split across literals
// ("arn:" + "aws:") or formatted from a partition-free pattern with an "aws"
// argument (fmt.Sprintf("arn:%s:…", "aws")) escapes it.
func TestNoCommercialARNLiterals(t *testing.T) {
	fset := token.NewFileSet()
	files := productionFiles(t, fset)
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s: unquote %s: %v", fset.Position(lit.Pos()), lit.Value, err)
				return true
			}
			if isCommercialARNLiteral(v) {
				t.Errorf("%s: %s hard-codes the aws partition; build the ARN with arnFor and acct.partition(), match it with parseARN or isARNOf",
					fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	}
}

// productionFiles parses every non-test .go file of the package.
func productionFiles(t *testing.T, fset *token.FileSet) []*ast.File {
	t.Helper()
	var files []*ast.File
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		t.Fatalf("walk package sources: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("parsed no production files; the walk most likely read the wrong directory")
	}
	return files
}

// partitionTakers returns, by name, the package's functions and methods whose
// first parameter is named partition, read from the source so a new builder
// is guarded without a list to update.
func partitionTakers(files []*ast.File) (funcs, methods map[string]bool) {
	funcs, methods = map[string]bool{}, map[string]bool{}
	for _, file := range files {
		for _, d := range file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || len(fd.Type.Params.List) == 0 {
				continue
			}
			first := fd.Type.Params.List[0]
			if len(first.Names) == 0 || first.Names[0].Name != "partition" {
				continue
			}
			if fd.Recv != nil {
				methods[fd.Name.Name] = true
			} else {
				funcs[fd.Name.Name] = true
			}
		}
	}
	return funcs, methods
}

// TestPartitionArgumentIsNeverALiteral fails when production code passes a
// string literal as the partition of any function or method in the package
// that takes one first. The partition is the account's (acct.partition()); a
// literal "aws" builds the commercial form in every partition and escapes
// TestNoCommercialARNLiterals, which only sees literals that already
// spell "arn:aws:". Methods are matched by name alone, so a same-named method
// of another type called with a literal first argument also fails.
func TestPartitionArgumentIsNeverALiteral(t *testing.T) {
	fset := token.NewFileSet()
	files := productionFiles(t, fset)
	funcs, methods := partitionTakers(files)
	for _, want := range []string{"arnFor", "ec2ARNIn", "s3BucketARN", "codeDeployARN"} {
		if !funcs[want] {
			t.Fatalf("partitionTakers() funcs = %v; missing %s, so the derivation read the wrong files or parameter", funcs, want)
		}
	}
	if !methods["refs"] {
		t.Fatalf("partitionTakers() methods = %v; missing mlRefAttrs.refs", methods)
	}
	calls := 0
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			var name string
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if !funcs[fn.Name] {
					return true
				}
				name = fn.Name
			case *ast.SelectorExpr:
				if !methods[fn.Sel.Name] {
					return true
				}
				name = fn.Sel.Name
			default:
				return true
			}
			calls++
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				t.Errorf("%s: %s(%s, …) hard-codes the partition; pass acct.partition()",
					fset.Position(lit.Pos()), name, lit.Value)
			}
			return true
		})
	}
	if calls == 0 {
		t.Fatal("found no calls to functions taking a partition; the walk most likely read no files")
	}
}

// TestIsCommercialARNLiteral pins the literal predicate, including the
// partition-aware forms it must not count.
func TestIsCommercialARNLiteral(t *testing.T) {
	for v, want := range map[string]bool{
		"arn:aws:ec2:%s:%s:instance/%s": true,
		"arn:aws":                       true,
		"prefix arn:aws:s3:::":          true,
		"arn:aws-cn:ec2:":               false,
		"arn:aws-us-gov:iam::":          false,
		"arn:":                          false,
		"arn:%s:ec2:":                   false,
	} {
		if got := isCommercialARNLiteral(v); got != want {
			t.Errorf("isCommercialARNLiteral(%q) = %v; want %v", v, got, want)
		}
	}
}
