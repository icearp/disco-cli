package main

import (
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/coverage"
)

func TestSplitWordsKebabPascal(t *testing.T) {
	cases := []struct {
		in            string
		kebab, pascal string
	}{
		{"Topic", "topic", "Topic"},
		{"RestApi", "rest-api", "RestApi"},
		{"virtualMachines", "virtual-machines", "VirtualMachines"},
		{"resource-name", "resource-name", "ResourceName"},
		{"Chromeosdevice", "chromeosdevice", "Chromeosdevice"},
		{"RESTApi", "rest-api", "RestApi"},
		{"S3Bucket", "s3-bucket", "S3Bucket"},
		{"AuthorizedOrgsDesc", "authorized-orgs-desc", "AuthorizedOrgsDesc"},
	}
	for _, c := range cases {
		if got := kebab(c.in); got != c.kebab {
			t.Errorf("kebab(%q) = %q; want %q", c.in, got, c.kebab)
		}
		if got := pascal(c.in); got != c.pascal {
			t.Errorf("pascal(%q) = %q; want %q", c.in, got, c.pascal)
		}
	}
}

func TestResourceSegments(t *testing.T) {
	cases := []struct {
		key, service string
		want         []string
	}{
		{"ec2/instance", "ec2", []string{"instance"}},
		{"kms/grant", "kms", []string{"grant"}},
		{"pubsub/topics", "pubsub", []string{"topic"}},
		{"microsoft.compute/virtualmachines", "microsoft.compute", []string{"virtualmachine"}},
		// The parent segment stays: virtualmachines/runcommands and
		// virtualmachinescalesets/virtualmachines/runcommands are different
		// resources and used to produce the same declaration twice.
		{"microsoft.network/virtualnetworks/subnets", "microsoft.network", []string{"virtualnetwork", "subnet"}},
		{"microsoft.compute/virtualmachinescalesets/virtualmachines/runcommands", "microsoft.compute", []string{"virtualmachinescaleset", "virtualmachine", "runcommand"}},
		{"compute/regiondisks", "compute", []string{"regiondisk"}},
		// Shapes only the irregular table spells (sdkinv.Singular, #16).
		{"monitoring/timeseries", "monitoring", []string{"timeseries"}},
		{"kendra/thesaurus", "kendra", []string{"thesaurus"}},
		{"qbusiness/indices", "qbusiness", []string{"index"}},
	}
	for _, c := range cases {
		if got := resourceSegments(c.key, c.service); !slices.Equal(got, c.want) {
			t.Errorf("resourceSegments(%q) = %v; want %v", c.key, got, c.want)
		}
	}
}

// TestGenScaffold_RefusesDuplicateAndExistingTypes: a type string this
// provider already declares, or one the rows would declare twice, is a compile
// error in the emitted file that format.Source does not catch.
func TestGenScaffold_RefusesDuplicateAndExistingTypes(t *testing.T) {
	rows := []coverage.Row{
		{Service: "pubsub", Key: "pubsub/topics", Bucket: coverage.BucketUncovered},
	}
	opts := scaffoldOpts{existingTypes: map[string]bool{"gcp:pubsub:topic": true}}
	if _, err := genScaffold("gcp", "pubsub", rows, opts); err == nil {
		t.Error("an already-declared type must refuse the scaffold")
	}
}

// TestGenScaffold_SkipsRegistrationWhenServiceExists: a second registerService
// panics the provider at init and a second func scan<Svc> does not compile.
func TestGenScaffold_SkipsRegistrationWhenServiceExists(t *testing.T) {
	rows := []coverage.Row{{Service: "pubsub", Key: "pubsub/topics", Bucket: coverage.BucketUncovered}}
	src, err := genScaffold("gcp", "pubsub", rows, scaffoldOpts{serviceRegistered: true, existingTypes: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"registerService(", "func scanPubsub("} {
		if strings.Contains(src, unwanted) {
			t.Errorf("scaffold emits %q for an already-registered service:\n%s", unwanted, src)
		}
	}
	if !strings.Contains(src, "TypePubsubTopic") {
		t.Errorf("scaffold dropped the types too:\n%s", src)
	}
}

func TestGenScaffold(t *testing.T) {
	rows := []coverage.Row{
		{Service: "pubsub", Key: "pubsub/topics", Bucket: coverage.BucketUncovered, Ops: []string{"pubsub:projects.topics.list"}, Scope: "project"},
		{Service: "pubsub", Key: "pubsub/topics/subscriptions", Bucket: coverage.BucketUncovered, Ops: []string{"pubsub:projects.topics.subscriptions.list"}, Depth: 1, Parent: "pubsub/topics", Scope: "project"},
	}
	src, err := genScaffold("gcp", "pubsub", rows, scaffoldOpts{existingTypes: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := format.Source([]byte(src)); err != nil {
		t.Fatalf("generated source does not gofmt/compile-parse: %v\n%s", err, src)
	}
	for _, want := range []string{
		// gofmt aligns the const block, so match the value, not the spacing.
		`= "gcp:pubsub:topic"`,
		`= "gcp:pubsub:topic:subscription"`,
		"TypePubsubTopic ",
		"TypePubsubTopicSubscription ",
		`// pubsub/topics: ops pubsub:projects.topics.list; depth 0; scope project`,
		`registerType(restype.Descriptor{Type: TypePubsubTopic, Service: "pubsub"})`,
		`// pubsub/topics/subscriptions: ops pubsub:projects.topics.subscriptions.list; depth 1 (parent pubsub/topics); scope project`,
		"func scanPubsub(ctx context.Context, p *project",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("scaffold lacks %q:\n%s", want, src)
		}
	}
}

// TestGenScaffold_EveryProviderStubsAScanner: the stub signature lives on each
// provider's coverage.ScaffoldStubber, and a provider that drops it degrades
// silently to descriptors only. An unknown provider keeps that fallback.
func TestGenScaffold_EveryProviderStubsAScanner(t *testing.T) {
	rows := []coverage.Row{{Service: "svc", Key: "svc/things", Bucket: coverage.BucketUncovered}}
	for _, p := range coverage.Names() {
		src, err := genScaffold(p, "svc", rows, scaffoldOpts{existingTypes: map[string]bool{}})
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !strings.Contains(src, "func scanSvc(") || !strings.Contains(src, "TODO: ") {
			t.Errorf("%s: no stub scanner; does its coverage provider implement ScaffoldStubber?\n%s", p, src)
		}
		if _, err := format.Source([]byte(src)); err != nil {
			t.Errorf("%s: stub does not parse: %v\n%s", p, err, src)
		}
	}
	src, err := genScaffold("nosuch", "svc", rows, scaffoldOpts{existingTypes: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, "func scanSvc(") || strings.Contains(src, "registerService(") {
		t.Errorf("unknown provider emitted a scanner:\n%s", src)
	}
}

// TestScannerStubMatchesServiceEntry: a stub whose signature is not the
// provider's serviceEntry.fn type parses fine yet does not compile in the
// provider package. Compare parameter and result types, names ignored.
func TestScannerStubMatchesServiceEntry(t *testing.T) {
	for _, p := range coverage.Names() {
		prov, _ := coverage.Get(p)
		s, ok := prov.(coverage.ScaffoldStubber)
		if !ok {
			continue // TestGenScaffold_EveryProviderStubsAScanner reports it
		}
		_, sig, _ := s.ScannerStub()
		expr, err := parser.ParseExpr("func" + sig)
		if err != nil {
			t.Fatalf("%s: stub signature does not parse: %v", p, err)
		}
		want := serviceEntryFn(t, filepath.Join("..", "..", "internal", "providers", p))
		if got := funcTypes(expr.(*ast.FuncType)); got != funcTypes(want) {
			t.Errorf("%s: stub signature %s, serviceEntry.fn %s", p, got, funcTypes(want))
		}
	}
}

// serviceEntryFn finds the fn field of the package's serviceEntry struct.
func serviceEntryFn(t *testing.T, dir string) *ast.FuncType {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var fn *ast.FuncType
		ast.Inspect(af, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "serviceEntry" {
				return fn == nil
			}
			for _, fld := range ts.Type.(*ast.StructType).Fields.List {
				if ft, ok := fld.Type.(*ast.FuncType); ok && len(fld.Names) == 1 && fld.Names[0].Name == "fn" {
					fn = ft
				}
			}
			return false
		})
		if fn != nil {
			return fn
		}
	}
	t.Fatalf("no serviceEntry.fn in %s", dir)
	return nil
}

// funcTypes renders a func type's parameter and result types without names.
func funcTypes(ft *ast.FuncType) string {
	list := func(fl *ast.FieldList) string {
		var out []string
		if fl == nil {
			return ""
		}
		for _, f := range fl.List {
			var b strings.Builder
			_ = printer.Fprint(&b, token.NewFileSet(), f.Type)
			for range max(1, len(f.Names)) {
				out = append(out, b.String())
			}
		}
		return strings.Join(out, ", ")
	}
	return "(" + list(ft.Params) + ") (" + list(ft.Results) + ")"
}
