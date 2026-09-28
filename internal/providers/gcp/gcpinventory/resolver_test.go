package gcpinventory

import (
	"path/filepath"
	"strings"
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

func TestWalkGCP(t *testing.T) {
	res, unpaired := walkFixture(t)
	pairingtest.ExpectPairing(t, res, "widgets/widgets", "scanWidgets", "emits", "gcp:widgets:part", "gcp:widgets:widget")
	pairingtest.ExpectPairing(t, res, "widgets/widgets/parts", "scanParts", "emits", "gcp:widgets:part")
	pairingtest.ExpectPairing(t, res, "widgets/gizmos", "gizmoScan.scanGizmos", "emits", "gcp:widgets:gizmo")
	pairingtest.ExpectPairing(t, res, "widgets/zones", "gizmoScan.scanZone", "emits", "gcp:widgets:zone")
	pairingtest.ExpectPairing(t, res, "widgets/buckets", "scanBuckets", "emits", "gcp:widgets:bucket")
	// A beta-only collection anchors through its own versioned import.
	pairingtest.ExpectPairing(t, res, "widgets/gadgets", "scanGadgets", "emits", "gcp:widgets:gadget")
	// A non-lister on a bound service is an "other" pairing.
	pairingtest.ExpectPairing(t, res, "widgets:projects.locations.widgets.get", "describeWidget", "other", "gcp:widgets:widget-detail")

	// run's label resolves through the method value it hands the driver.
	if got := pairingtest.DiagKinds(res); len(got) != 1 || got["label-no-anchor"] != 1 {
		t.Errorf("diagnostics = %v", res.Diagnostics)
	}
	pairingtest.ExpectDiag(t, res, "label-no-anchor", "widgets_scanners.go", 111)
	if len(unpaired) != 1 || unpaired["gcp:widgets:widget-detail"] != "other-op:widgets:projects.locations.widgets.get" {
		t.Errorf("unpaired = %v", unpaired)
	}
}

func TestResolverKeys(t *testing.T) {
	gcp := gcpResolver{}
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
