package pairing

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
	_ "github.com/icearp/disco-cli/internal/sdkinv/aws"
	_ "github.com/icearp/disco-cli/internal/sdkinv/azure"
	_ "github.com/icearp/disco-cli/internal/sdkinv/gcp"
)

// walkFixture pairs the synthetic scanner package for provider against the
// extractor's own fixture universe.
func walkFixture(t *testing.T, provider string) (*Result, map[string]string) {
	t.Helper()
	ext, ok := sdkinv.Get(provider)
	if !ok {
		t.Fatalf("extractor %s not registered", provider)
	}
	u, err := ext.Extract(context.Background(), filepath.Join("..", provider, "testdata", "cache"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("testdata", "scannerpkg", provider)
	res, err := Walk(dir, u)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := Get(provider)
	sdk, err := SDKFiles(dir, r)
	if err != nil {
		t.Fatal(err)
	}
	return res, res.Unpaired(res.Consts, sdk)
}

type want struct {
	fn    string
	kind  string
	types []string
}

// pairingsFor returns the pairings for a candidate key or "other" label.
func pairingsFor(res *Result, key string) map[string]want {
	out := map[string]want{}
	for _, p := range res.Pairings {
		if p.Key == key || (p.Key == "" && p.Label == key) {
			out[p.Func] = want{p.Func, p.Kind, p.Types}
		}
	}
	return out
}

func diagKinds(res *Result) map[string]int {
	m := map[string]int{}
	for _, d := range res.Diagnostics {
		m[d.Kind]++
	}
	return m
}

func expectPairing(t *testing.T, res *Result, key, fn, kind string, types ...string) {
	t.Helper()
	got, ok := pairingsFor(res, key)[fn]
	if !ok {
		t.Errorf("%s: no pairing from %s; have %v", key, fn, pairingsFor(res, key))
		return
	}
	sort.Strings(types)
	if got.kind != kind || strings.Join(got.types, ",") != strings.Join(types, ",") {
		t.Errorf("%s from %s = %s %v, want %s %v", key, fn, got.kind, got.types, kind, types)
	}
}

func expectDiag(t *testing.T, res *Result, kind, file string, line int) {
	t.Helper()
	for _, d := range res.Diagnostics {
		if d.Kind == kind && d.File == file && d.Line == line {
			return
		}
	}
	t.Errorf("no %s diagnostic at %s:%d; have %v", kind, file, line, res.Diagnostics)
}

func TestWalkAWS(t *testing.T) {
	res, unpaired := walkFixture(t, "aws")
	// Own anchor; types reach through the callee.
	expectPairing(t, res, "widgets/widget", "scanWidgets", "emits", TypeAWSWidget, TypeAWSGrant)
	expectPairing(t, res, "widgets/grant", "scanGrants", "emits", TypeAWSGrant)
	// A helper that only lists pairs with what its caller stores.
	expectPairing(t, res, "widgets/gadget", "listGadgets", "sidecar")
	expectPairing(t, res, "widgets/gadget", "scanGadgets", "emits", TypeAWSGadget)
	// A dispatcher pairs the rows no anchored callee stores with all it lists.
	expectPairing(t, res, "widgets/widget", "scanAll", "derived", TypeAWSSchema)
	expectPairing(t, res, "widgets/gadget", "scanAll", "derived", TypeAWSSchema)
	// A type constant flows into the helper it is passed to.
	expectPairing(t, res, "widgets/widget", "storeTyped", "emits", TypeAWSWidget)
	// An attribute read pairs like any op.
	expectPairing(t, res, "widgets/accountsetting", "scanAccountSettings", "sidecar")
	// A label with no call anywhere still records the intent.
	expectPairing(t, res, "widgets/zone", "staleLabel", "label")
	// A shared store helper keeps each caller's type with that caller's op.
	expectPairing(t, res, "widgets/alias", "scanAliases", "emits", TypeAWSAlias)
	expectPairing(t, res, "widgets/gizmo", "scanGizmos", "emits", TypeAWSGizmo)
	// A package-level op/type table pairs with every op the ranging scanner calls.
	expectPairing(t, res, "widgets/gizmo", "scanTable", "emits", TypeAWSTableGizmo, TypeAWSTableZone)
	expectPairing(t, res, "widgets/zone", "scanTable", "emits", TypeAWSTableGizmo, TypeAWSTableZone)

	if got := diagKinds(res); len(got) != 2 || got["label-no-op"] != 1 || got["label-no-anchor"] != 1 {
		t.Errorf("diagnostics = %v", res.Diagnostics)
	}
	expectDiag(t, res, "label-no-op", "widgets_scanners.go", 110)
	expectDiag(t, res, "label-no-anchor", "widgets_scanners.go", 114)
	// reportDefaults' label resolves through its caller: no diagnostic.
	for _, d := range res.Diagnostics {
		if strings.Contains(d.Message, "GetAccountSettings") {
			t.Errorf("caller-resolved label diagnosed: %v", d)
		}
	}
	if len(unpaired) != 3 || unpaired[TypeAWSOrphan] != "unexplained" || unpaired[TypeAWSGraphOnly] != "non-sdk" || unpaired[TypeAWSMasked] != "unexplained" {
		t.Errorf("unpaired = %v", unpaired)
	}
	if _, ok := res.Consts["serviceLabel"]; ok {
		t.Error("non-Type constant collected")
	}
}

const (
	TypeAWSWidget     = "aws:widgets:widget"
	TypeAWSGrant      = "aws:widgets:grant"
	TypeAWSGadget     = "aws:widgets:gadget"
	TypeAWSSchema     = "aws:widgets:schema"
	TypeAWSOrphan     = "aws:widgets:orphan"
	TypeAWSGraphOnly  = "aws:graph:thing"
	TypeAWSAlias      = "aws:widgets:alias"
	TypeAWSGizmo      = "aws:widgets:gizmo"
	TypeAWSTableGizmo = "aws:widgets:table-gizmo"
	TypeAWSTableZone  = "aws:widgets:table-zone"
	TypeAWSMasked     = "aws:widgets:masked"
)

func TestWalkAzure(t *testing.T) {
	res, unpaired := walkFixture(t, "azure")
	// The pager is built by the caller, stored by the callee.
	expectPairing(t, res, "microsoft.widgets/widgets", "scanWidgets", "emits", "azure:microsoft.widgets:widgets")
	// A local interface seam binds like the client it stands in for.
	expectPairing(t, res, "microsoft.widgets/widgets/parts", "scanParts", "emits", "azure:microsoft.widgets:widgets:parts")
	// Client factory and struct-field receivers.
	expectPairing(t, res, "microsoft.widgets/tenantthings", "scanTenantThings", "emits", "azure:microsoft.widgets:tenantthings")
	expectPairing(t, res, "microsoft.widgets/skus", "skuScan.scanSKUs", "emits", "azure:microsoft.widgets:skus")
	// A call the pinned SDK no longer ships is skew, not a typo.
	expectPairing(t, res, "WidgetsClient.ListGone", "scanStale", "skew", "azure:microsoft.widgets:stale")

	got := diagKinds(res)
	if got["label-no-op"] != 0 || got["label-no-anchor"] != 0 {
		t.Errorf("diagnostics = %v", res.Diagnostics)
	}
	expectDiag(t, res, "sdk-skew", "widgets_scanners.go", 89)
	expectDiag(t, res, "sdk-skew", "widgets_scanners.go", 93)
	expectDiag(t, res, "unresolved-receiver", "widgets_scanners.go", 104)
	expectDiag(t, res, "sdk-module-absent", "gone_scanners.go", 12)
	if len(unpaired) != 2 || unpaired["azure:microsoft.entra:user"] != "non-sdk" || unpaired["azure:microsoft.widgets:stale"] != "sdk-skew:WidgetsClient.ListGone" {
		t.Errorf("unpaired = %v", unpaired)
	}
}

func TestWalkGCP(t *testing.T) {
	res, unpaired := walkFixture(t, "gcp")
	expectPairing(t, res, "widgets/widgets", "scanWidgets", "emits", "gcp:widgets:part", "gcp:widgets:widget")
	expectPairing(t, res, "widgets/widgets/parts", "scanParts", "emits", "gcp:widgets:part")
	expectPairing(t, res, "widgets/gizmos", "gizmoScan.scanGizmos", "emits", "gcp:widgets:gizmo")
	expectPairing(t, res, "widgets/zones", "gizmoScan.scanZone", "emits", "gcp:widgets:zone")
	expectPairing(t, res, "widgets/buckets", "scanBuckets", "emits", "gcp:widgets:bucket")
	// A beta-only collection anchors through its own versioned import.
	expectPairing(t, res, "widgets/gadgets", "scanGadgets", "emits", "gcp:widgets:gadget")
	// A non-lister on a bound service is an "other" pairing.
	expectPairing(t, res, "widgets:projects.locations.widgets.get", "describeWidget", "other", "gcp:widgets:widget-detail")

	// run's label resolves through the method value it hands the driver.
	if got := diagKinds(res); len(got) != 1 || got["label-no-anchor"] != 1 {
		t.Errorf("diagnostics = %v", res.Diagnostics)
	}
	expectDiag(t, res, "label-no-anchor", "widgets_scanners.go", 111)
	if len(unpaired) != 1 || unpaired["gcp:widgets:widget-detail"] != "other-op:widgets:projects.locations.widgets.get" {
		t.Errorf("unpaired = %v", unpaired)
	}
}

func TestDefaultImportName(t *testing.T) {
	for path, want := range map[string]string{
		"github.com/aws/aws-sdk-go-v2/service/ec2":                                    "ec2",
		"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6": "armcompute",
		"google.golang.org/api/compute/v1":                                            "compute",
		"google.golang.org/api/admin/directory/v1":                                    "directory",
	} {
		if got := defaultImportName(path); got != want {
			t.Errorf("defaultImportName(%s) = %s, want %s", path, got, want)
		}
	}
}

func TestSkewedAndModuleAbsent(t *testing.T) {
	if !skewed("armcompute:CloudServices.ListAll", []string{"CloudServicesClient.ListAll"}) {
		t.Error("skewed: client suffix not ignored")
	}
	if skewed("armcompute:VirtualMachines.ListAll", []string{"CloudServicesClient.ListAll"}) {
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

func TestResolverKeys(t *testing.T) {
	aws, _ := Get("aws")
	if aws.ImportKey("github.com/aws/aws-sdk-go-v2/service/ec2/types") != "" || aws.ImportKey("github.com/aws/aws-sdk-go-v2/service/ec2") != "ec2" {
		t.Error("aws ImportKey")
	}
	if mod, op := aws.OpKey(sdkinv.Operation{Name: "ListAccessPoints", Module: "aws-sdk-go-v2@x/codegen/sdk-codegen/aws-models/s3-control.json"}); mod != "s3control" || op != "ListAccessPoints" {
		t.Errorf("aws OpKey = %s %s", mod, op)
	}
	if got := aws.LabelAliases(sdkinv.Candidate{}, sdkinv.Operation{Service: "access-analyzer", Name: "ListAnalyzers", Label: "access-analyzer:ListAnalyzers"}); got[1] != "accessanalyzer:ListAnalyzers" {
		t.Errorf("aws LabelAliases = %v", got)
	}

	azure, _ := Get("azure")
	for path, want := range map[string]string{
		"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6":      "armcompute",
		"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake": "",
		"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime":                             "",
	} {
		if got := azure.ImportKey(path); got != want {
			t.Errorf("azure ImportKey(%s) = %q, want %q", path, got, want)
		}
	}
	if ident, ok := azure.TypeOwner("armquota", "ClientListResponse"); !ok || ident != "Client" {
		t.Errorf("azure TypeOwner bare client = %s %v", ident, ok)
	}
	if ident, ok := azure.TypeOwner("armsql", "ServersClientListOptions"); !ok || ident != "ServersClient" {
		t.Errorf("azure TypeOwner = %s %v", ident, ok)
	}
	if _, ok := azure.TypeOwner("armsql", "Server"); ok {
		t.Error("azure TypeOwner: model type bound")
	}

	gcp, _ := Get("gcp")
	if gcp.ImportKey("google.golang.org/api/option") != "" || gcp.ImportKey("google.golang.org/api/admin/directory/v1") != "admin" {
		t.Error("gcp ImportKey")
	}
	if mod, _ := gcp.OpKey(sdkinv.Operation{Name: "users.list", Module: "google.golang.org/api@v0.1.0/admin/directory/v1"}); mod != "admin" {
		t.Errorf("gcp OpKey = %s", mod)
	}
	got := gcp.LabelAliases(sdkinv.Candidate{Service: "cloudkms", Key: "cloudkms/keyrings"},
		sdkinv.Operation{Service: "cloudkms", Name: "projects.locations.keyRings.list", Label: "cloudkms:projects.locations.keyRings.list"})
	want := []string{"cloudkms:projects.locations.keyRings.list", "cloudkms:keyRings.list", "cloudkms:keyRings.list"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("gcp LabelAliases = %v, want %v", got, want)
	}
}
