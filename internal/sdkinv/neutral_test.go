package sdkinv

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// providerWordRe names clouds and their SDK formats. Code in this tree must
// not mention one: provider knowledge lives in internal/providers/<p>/<p>inventory,
// so adding a provider never edits the core. Comments may cite examples.
var providerWordRe = regexp.MustCompile(`(?i)aws|azure|gcp|google|microsoft|amazon|smithy|discovery|\barm|\barn|sigv4|resourcemanager|codegen`)

// TestCoreIsProviderNeutral scans the identifiers and string literals of every
// non-test Go file under internal/sdkinv.
func TestCoreIsProviderNeutral(t *testing.T) {
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "testdata" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		var s scanner.Scanner
		s.Init(fset.AddFile(p, -1, len(src)), src, nil, 0)
		for {
			pos, tok, lit := s.Scan()
			if tok == token.EOF {
				return nil
			}
			if (tok == token.IDENT || tok == token.STRING) && providerWordRe.MatchString(lit) {
				t.Errorf("%s: provider-specific %s %s", fset.Position(pos), tok, lit)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
