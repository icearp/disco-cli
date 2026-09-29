package pairing

import (
	"fmt"
	"go/ast"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

func TestImportName(t *testing.T) {
	used := map[string]bool{"ec2": true, "armcompute": true, "compute": true, "admin": true}
	for path, want := range map[string]string{
		"github.com/aws/aws-sdk-go-v2/service/ec2":                                    "ec2",
		"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6": "armcompute",
		"google.golang.org/api/compute/v1":                                            "compute",
		"google.golang.org/api/admin/directory/v1":                                    "admin", // package admin
		"example.com/unused/v2":                                                       "v2",    // no selector names it
	} {
		if got := importName(path, used); got != want {
			t.Errorf("importName(%s) = %s, want %s", path, got, want)
		}
	}
}

func TestSkewedAndModuleAbsent(t *testing.T) {
	if !skewed("CloudServicesClient.ListAll", []string{"CloudServicesClient.ListAll"}) {
		t.Error("skewed: same op not matched")
	}
	if skewed("VirtualMachinesClient.ListAll", []string{"CloudServicesClient.ListAll"}) {
		t.Error("skewed: unrelated op matched")
	}
	modules := map[string]bool{"armcompute": true}
	if !moduleAbsent("armappplatform:Services.ListBySubscription", []string{"armcompute", "armappplatform"}, modules) {
		t.Error("moduleAbsent: absent import not reported")
	}
	if moduleAbsent("armcompute:VMs.ListAll", []string{"armcompute"}, modules) {
		t.Error("moduleAbsent: present module reported")
	}
}

// fakeResolver is a provider the core knows nothing about: its SDK is
// example.com/fakesdk/<module>, and a call <module>.<Op>( anchors Op. Pairing
// it end to end proves the walk carries no provider knowledge.
type fakeResolver struct{}

func init() { Register(fakeResolver{}) }

var fakeLabelRe = regexp.MustCompile(`^[a-z]+:[A-Za-z]+$`)

func (fakeResolver) Name() string                 { return "fake" }
func (fakeResolver) LabelGrammar() *regexp.Regexp { return fakeLabelRe }
func (fakeResolver) ImportKey(path string) string {
	mod, _ := strings.CutPrefix(path, "example.com/fakesdk/")
	if mod == path {
		return ""
	}
	return mod
}
func (fakeResolver) OpKey(op sdkinv.Operation) (string, string) { return op.Module, op.Name }
func (fakeResolver) LabelAliases(_ sdkinv.Candidate, op sdkinv.Operation) []string {
	return []string{op.Label}
}
func (fakeResolver) Constructor(_, _ string) (string, bool)           { return "", false }
func (fakeResolver) TypeOwner(_, _ string, _ []string) (string, bool) { return "", false }
func (fakeResolver) LabelOp(lit string) string {
	_, op, _ := strings.Cut(lit, ":")
	return op
}

func (fakeResolver) Anchors(f *Func) ([]Anchor, []Diagnostic) {
	var out []Anchor
	ast.Inspect(f.Decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			if mod := (fakeResolver{}).ImportKey(f.Imports[id.Name]); mod != "" && (f.Ops(mod, sel.Sel.Name) || f.Other(mod, sel.Sel.Name)) {
				out = append(out, Anchor{Module: mod, Op: sel.Sel.Name, Line: f.Line(call)})
			}
		}
		return true
	})
	return out, nil
}

func TestWalkFakeProvider(t *testing.T) {
	op := func(name string, list bool) sdkinv.Operation {
		return sdkinv.Operation{Service: "widgets", Name: name, Label: "widgets:" + name, IsList: list, Module: "widgets"}
	}
	u := &sdkinv.Universe{
		Provider: "fake",
		Candidates: []sdkinv.Candidate{{
			Provider: "fake", Service: "widgets", Key: "widgets/widget", Class: sdkinv.ClassResource,
			Ops: []sdkinv.Operation{op("ListWidgets", true)},
		}},
		Other: []sdkinv.Operation{op("GetWidget", false)},
	}
	dir := filepath.Join("testdata", "scannerpkg")
	res, err := Walk(dir, u)
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := SDKFiles(dir, fakeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	unpaired := res.Unpaired(res.Consts, sdk)
	var got []string
	for _, p := range res.Pairings {
		got = append(got, fmt.Sprintf("%s %s %s %v", p.Func, p.Kind, p.Op, p.Types))
	}
	want := []string{
		"scanWidgets emits ListWidgets [fake:widgets:widget]",
		"describeWidget other GetWidget [fake:widgets:detail]",
		"scanAll derived ListWidgets [fake:widgets:tally]", // not fake:gizmos:note: another service
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("pairings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Kind != "label-no-op" {
		t.Errorf("diagnostics = %v", res.Diagnostics)
	}
	if len(unpaired) != 3 || unpaired["fake:widgets:orphan"] != "unexplained" || unpaired["fake:gizmos:note"] != "unexplained" {
		t.Errorf("unpaired = %v", unpaired)
	}
}
