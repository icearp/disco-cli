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

// commercialARNLiteralCeiling is the number of string literals outside
// aws_arn.go that hard-code the aws partition into an ARN. Each one builds or
// matches the commercial form only, so in aws-cn or aws-us-gov it names a
// resource that does not exist. New code builds ARNs through the aws_arn.go
// helpers with the account's or parent ARN's partition; the ceiling only
// falls.
const commercialARNLiteralCeiling = 474

// isCommercialARNLiteral reports whether a string literal's value hard-codes
// the aws partition: it contains "arn:aws:" or is exactly "arn:aws" (the bare
// prefix some matchers test with HasPrefix).
func isCommercialARNLiteral(v string) bool {
	return v == "arn:aws" || strings.Contains(v, "arn:aws:")
}

// TestCommercialARNLiteralRatchet counts commercial-partition ARN literals in
// the package's production files. It reads string literals from the AST, so
// comments never count, and it counts single literals only: a commercial ARN
// split across literals ("arn:" + "aws:") or formatted from a partition-free
// pattern with an "aws" argument (fmt.Sprintf("arn:%s:…", "aws")) escapes it.
// It fails in both directions: above the ceiling is a new hard-coded
// partition; below it is a removal the ceiling must record, or the slack would
// let the next literal in unnoticed.
func TestCommercialARNLiteralRatchet(t *testing.T) {
	fset := token.NewFileSet()
	count := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || path == "aws_arn.go" {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
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
				count++
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk package sources: %v", err)
	}
	if count == 0 {
		t.Fatal("found no commercial ARN literals at all; the walk most likely read no files")
	}

	switch {
	case count > commercialARNLiteralCeiling:
		t.Errorf("%d string literals hard-code the aws partition into an ARN, above the ceiling of %d; "+
			"build the new ARN with arnFor or an aws_arn.go helper and the account's partition",
			count, commercialARNLiteralCeiling)
	case count < commercialARNLiteralCeiling:
		t.Errorf("%d string literals hard-code the aws partition into an ARN, below the ceiling of %d; "+
			"lower commercialARNLiteralCeiling to %d so the removal stays removed",
			count, commercialARNLiteralCeiling, count)
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
