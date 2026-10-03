package azureinventory

import (
	"path/filepath"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/icearp/disco-cli/internal/sdkinv/pairing"
	"github.com/icearp/disco-cli/internal/sdkinv/pairing/pairingtest"
)

// walkFixture pairs testdata/scannerpkg against the extractor's own fixture
// universe.
func walkFixture(t *testing.T) (*pairing.Result, map[string]string) {
	t.Helper()
	return pairingtest.WalkFixture(t, extractor{}, filepath.Join("testdata", "cache"), filepath.Join("testdata", "scannerpkg"))
}

func TestWalkAzure(t *testing.T) {
	res, unpaired := walkFixture(t)
	// The pager is built by the caller, stored by the callee.
	pairingtest.ExpectPairing(t, res, "microsoft.widgets/widgets", "scanWidgets", "emits", "azure:microsoft.widgets:widgets")
	// A local interface seam binds like the client it stands in for.
	pairingtest.ExpectPairing(t, res, "microsoft.widgets/widgets/parts", "scanParts", "emits", "azure:microsoft.widgets:widgets:parts")
	// Client factory and struct-field receivers.
	pairingtest.ExpectPairing(t, res, "microsoft.widgets/tenantthings", "scanTenantThings", "emits", "azure:microsoft.widgets:tenantthings")
	pairingtest.ExpectPairing(t, res, "microsoft.widgets/skus", "skuScan.scanSKUs", "emits", "azure:microsoft.widgets:skus")
	// Two modules share the basename armwidgets; each scanner pairs only with
	// the module its file imports.
	pairingtest.ExpectPairing(t, res, "microsoft.gadgetry/widgets", "scanGadgetryWidgets", "emits", "azure:microsoft.gadgetry:widgets")
	if _, ok := pairingtest.PairingsFor(res, "microsoft.widgets/widgets")["scanGadgetryWidgets"]; ok {
		t.Error("scanGadgetryWidgets paired with widgets/armwidgets' same-named op")
	}
	if _, ok := pairingtest.PairingsFor(res, "microsoft.gadgetry/widgets")["scanWidgets"]; ok {
		t.Error("scanWidgets paired with gadgetry/armwidgets' same-named op")
	}
	// A call the pinned SDK no longer ships is skew, not a typo.
	pairingtest.ExpectPairing(t, res, "WidgetsClient.ListGone", "scanStale", "skew", "azure:microsoft.widgets:stale")

	got := pairingtest.DiagKinds(res)
	if got["label-no-op"] != 0 || got["label-no-anchor"] != 0 {
		t.Errorf("diagnostics = %v", res.Diagnostics)
	}
	pairingtest.ExpectDiag(t, res, "sdk-skew", "widgets_scanners.go", 89)
	pairingtest.ExpectDiag(t, res, "sdk-skew", "widgets_scanners.go", 93)
	pairingtest.ExpectDiag(t, res, "unresolved-receiver", "widgets_scanners.go", 104)
	pairingtest.ExpectDiag(t, res, "sdk-module-absent", "gone_scanners.go", 12)
	// A label off the strict grammar but recognisably one: a typo, not silence.
	pairingtest.ExpectDiag(t, res, "label-malformed", "widgets_scanners.go", 119)
	if len(unpaired) != 2 || unpaired["azure:microsoft.entra:user"] != "non-sdk" || unpaired["azure:microsoft.widgets:stale"] != "sdk-skew:WidgetsClient.ListGone" {
		t.Errorf("unpaired = %v", unpaired)
	}
}

func TestResolverKeys(t *testing.T) {
	azure := azureResolver{}
	for path, want := range map[string]string{
		"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6":      "compute/armcompute",
		"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6/fake": "",
		"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime":                             "",
	} {
		if got := azure.ImportKey(path); got != want {
			t.Errorf("azure ImportKey(%s) = %q, want %q", path, got, want)
		}
	}
	// OpKey's module must equal ImportKey's for the same module, or no anchor
	// resolves; LabelModule gives back the "armX:" label prefix.
	op := sdkinv.Operation{Module: "azure-sdk-for-go@abc/sdk/resourcemanager/resources/armmanagedapplications", Name: "ApplicationsClient.Get"}
	if mod, name := azure.OpKey(op); mod != "resources/armmanagedapplications" || name != "ApplicationsClient.Get" {
		t.Errorf("azure OpKey = %q %q", mod, name)
	}
	if got := azure.LabelModule("resources/armmanagedapplications"); got != "armmanagedapplications" {
		t.Errorf("azure LabelModule = %q", got)
	}
	clients := []string{"Client", "ClientGroupsClient", "ServersClient"}
	for typ, want := range map[string]string{
		"ClientListResponse":             "Client",
		"ServersClientListOptions":       "ServersClient",
		"ClientGroupsClientListResponse": "ClientGroupsClient", // longest, not the bare Client
		"Server":                         "",                   // a model
		"ServersClient":                  "",                   // the client itself names no op type
		"UnknownClientListOptions":       "",                   // no known client
	} {
		if ident, ok := azure.TypeOwner("armsql", typ, clients); ident != want || ok != (want != "") {
			t.Errorf("azure TypeOwner(%s) = %q %v, want %q", typ, ident, ok, want)
		}
	}

	// Labels drop the client type's "Client" suffix; the base client keeps it.
	for lit, want := range map[string]string{"armcompute:CloudServices.ListAll": "CloudServicesClient.ListAll", "armquota:Client.List": "Client.List"} {
		if got := azure.LabelOp(lit); got != want {
			t.Errorf("azure LabelOp(%s) = %s, want %s", lit, got, want)
		}
	}
}
