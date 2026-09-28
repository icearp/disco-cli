package awsinventory

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

func TestWalkAWS(t *testing.T) {
	res, unpaired := walkFixture(t)
	// Own anchor. scanGrants is called with nothing but scanWidgets' own
	// parameters, so it cannot be storing rows this listing returned and its
	// type is not credited here — it has its own pairing below.
	pairingtest.ExpectPairing(t, res, "widgets/widget", "scanWidgets", "emits", TypeAWSWidget)
	pairingtest.ExpectPairing(t, res, "widgets/grant", "scanGrants", "emits", TypeAWSGrant)
	// A helper that only lists pairs with what its caller stores.
	pairingtest.ExpectPairing(t, res, "widgets/gadget", "listGadgets", "sidecar")
	pairingtest.ExpectPairing(t, res, "widgets/gadget", "scanGadgets", "emits", TypeAWSGadget)
	// A dispatcher pairs the rows no anchored callee stores with all it lists.
	pairingtest.ExpectPairing(t, res, "widgets/widget", "scanAll", "derived", TypeAWSSchema)
	pairingtest.ExpectPairing(t, res, "widgets/gadget", "scanAll", "derived", TypeAWSSchema)
	// A type constant flows into the helper it is passed to.
	pairingtest.ExpectPairing(t, res, "widgets/widget", "storeTyped", "emits", TypeAWSWidget)
	// An attribute read pairs like any op.
	pairingtest.ExpectPairing(t, res, "widgets/accountsetting", "scanAccountSettings", "sidecar")
	// A label with no call anywhere still records the intent.
	pairingtest.ExpectPairing(t, res, "widgets/zone", "staleLabel", "label")
	// A shared store helper keeps each caller's type with that caller's op.
	pairingtest.ExpectPairing(t, res, "widgets/alias", "scanAliases", "emits", TypeAWSAlias)
	pairingtest.ExpectPairing(t, res, "widgets/gizmo", "scanGizmos", "emits", TypeAWSGizmo)
	// A package-level op/type table pairs with every op the ranging scanner calls.
	pairingtest.ExpectPairing(t, res, "widgets/gizmo", "scanTable", "emits", TypeAWSTableGizmo, TypeAWSTableZone)
	pairingtest.ExpectPairing(t, res, "widgets/zone", "scanTable", "emits", TypeAWSTableGizmo, TypeAWSTableZone)

	// A resolver names types in a filter and in a switch, and writes only
	// edges: the anchor stands, the types do not.
	pairingtest.ExpectPairing(t, res, "widgets/alias", "resolveAliasLinks", "sidecar")
	// The store four plain hops below the anchor still pairs.
	pairingtest.ExpectPairing(t, res, "widgets/zone", "scanDeep", "emits", TypeAWSDeep)

	if got := pairingtest.DiagKinds(res); len(got) != 2 || got["label-no-op"] != 1 || got["label-no-anchor"] != 1 {
		t.Errorf("diagnostics = %v", res.Diagnostics)
	}
	pairingtest.ExpectDiag(t, res, "label-no-op", "widgets_scanners.go", 110)
	pairingtest.ExpectDiag(t, res, "label-no-anchor", "widgets_scanners.go", 114)
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
	TypeAWSDeep       = "aws:widgets:deep"
)

func TestWalkIsDeterministic(t *testing.T) {
	var first string
	for i := range 20 {
		res, _ := walkFixture(t)
		got := pairingtest.Dump(res)
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("walk %d differs from walk 0:\n%s", i, pairingtest.DiffLines(first, got))
		}
	}
	res, _ := walkFixture(t)
	if p, ok := pairingtest.PairingsFor(res, "widgets/widget")["scanWidgetPair"]; !ok {
		t.Fatalf("no pairing from scanWidgetPair; have %v", pairingtest.PairingsFor(res, "widgets/widget"))
	} else if p.Kind != "emits" {
		t.Errorf("scanWidgetPair kind = %s, want emits", p.Kind)
	}
	for _, p := range res.Pairings {
		// The listing wins over the detail read of the same candidate.
		if p.Func == "scanWidgetPair" && p.Op != "ListWidgets" {
			t.Errorf("scanWidgetPair op = %q, want ListWidgets", p.Op)
		}
	}
}

func TestResolverKeys(t *testing.T) {
	aws := awsResolver{}
	if aws.ImportKey("github.com/aws/aws-sdk-go-v2/service/ec2/types") != "" || aws.ImportKey("github.com/aws/aws-sdk-go-v2/service/ec2") != "ec2" {
		t.Error("aws ImportKey")
	}
	if mod, op := aws.OpKey(sdkinv.Operation{Name: "ListAccessPoints", Module: "aws-sdk-go-v2@x/codegen/sdk-codegen/aws-models/s3-control.json"}); mod != "s3control" || op != "ListAccessPoints" {
		t.Errorf("aws OpKey = %s %s", mod, op)
	}
	if got := aws.LabelAliases(sdkinv.Candidate{}, sdkinv.Operation{Service: "access-analyzer", Name: "ListAnalyzers", Label: "access-analyzer:ListAnalyzers"}); got[1] != "accessanalyzer:ListAnalyzers" {
		t.Errorf("aws LabelAliases = %v", got)
	}
}
