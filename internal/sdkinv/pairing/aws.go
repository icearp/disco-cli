package pairing

import (
	"go/ast"
	"regexp"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

const awsServicePrefix = "github.com/aws/aws-sdk-go-v2/service/"

var (
	awsLabelRe     = regexp.MustCompile(`^[a-z0-9-]+:[A-Z][A-Za-z0-9]+$`)
	awsPaginatorRe = regexp.MustCompile(`^New([A-Z]\w+)Paginator$`)
	awsInputRe     = regexp.MustCompile(`^([A-Z]\w+)Input$`)
)

type awsResolver struct{}

func init() { Register(awsResolver{}) }

func (awsResolver) Name() string                 { return "aws" }
func (awsResolver) LabelGrammar() *regexp.Regexp { return awsLabelRe }

// ImportKey: the service package directory name; sub-packages (types,
// document) carry no operations.
func (awsResolver) ImportKey(path string) string {
	rest, ok := strings.CutPrefix(path, awsServicePrefix)
	if !ok || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

// OpKey: the Smithy model file name with hyphens removed is the Go package
// name (iot-data-plane.json → iotdataplane).
func (awsResolver) OpKey(op sdkinv.Operation) (string, string) {
	file := op.Module[strings.LastIndex(op.Module, "/")+1:]
	return strings.ReplaceAll(strings.TrimSuffix(file, ".json"), "-", ""), op.Name
}

// LabelAliases: disco labels by its own service segment, which may differ
// from the signing name only in separators (accessanalyzer / access-analyzer).
func (awsResolver) LabelAliases(_ sdkinv.Candidate, op sdkinv.Operation) []string {
	return []string{op.Label, sdkinv.Canon(op.Service) + ":" + op.Name}
}

// TypeOwner: AWS anchors resolve by operation name over the imported
// modules, so seams need no binding.
func (awsResolver) TypeOwner(_, _ string) (string, bool) { return "", false }

func (awsResolver) Constructor(_, fn string) (string, bool) {
	if fn == "NewFromConfig" || fn == "New" {
		return "Client", true
	}
	return "", false
}

// Anchors: pkg.New<Op>Paginator(, pkg.<Op>Input{ and recv.<Op>( where <Op>
// is an operation of a service package the file imports.
func (awsResolver) Anchors(f *Func) ([]Anchor, []Diagnostic) {
	var out []Anchor
	add := func(mod, op string, n ast.Node) {
		out = append(out, Anchor{Module: mod, Op: op, Line: line(f, n)})
	}
	ast.Inspect(f.Decl.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			if sel, ok := x.Type.(*ast.SelectorExpr); ok {
				if mod := importMod(f, sel.X); mod != "" {
					if m := awsInputRe.FindStringSubmatch(sel.Sel.Name); m != nil {
						add(mod, m[1], x)
					}
				}
			}
		case *ast.CallExpr:
			sel, ok := x.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if mod := importMod(f, sel.X); mod != "" {
				if m := awsPaginatorRe.FindStringSubmatch(sel.Sel.Name); m != nil {
					add(mod, m[1], x)
				}
				return true
			}
			for _, mod := range f.SDKMods {
				if f.Ops(mod, sel.Sel.Name) || f.Other(mod, sel.Sel.Name) {
					add(mod, sel.Sel.Name, x)
				}
			}
		}
		return true
	})
	return out, nil
}

// importMod returns the module key when e is an SDK import identifier.
func importMod(f *Func, e ast.Expr) string {
	id, ok := e.(*ast.Ident)
	if !ok {
		return ""
	}
	path, ok := f.Imports[id.Name]
	if !ok {
		return ""
	}
	r, _ := Get(f.provider)
	return r.ImportKey(path)
}
