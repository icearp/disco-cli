package coverage

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/icearp/disco-cli/internal/sdkinv/pairing"
)

func cand(service, key string, class sdkinv.Class, depth int, signals ...string) sdkinv.Candidate {
	c := sdkinv.Candidate{
		Provider: "aws", Service: service, Key: key, Class: class, Depth: depth, Signals: signals, Rule: "sr-resource",
		Ops: []sdkinv.Operation{{Label: service + ":List" + key[strings.LastIndex(key, "/")+1:], Scope: sdkinv.ScopeRegion}},
	}
	if depth > 0 {
		c.Parent = key[:strings.LastIndex(key, "/")] + "/key" // the fixture's only child hangs off kms/key
	}
	return c
}

func testInputs() Inputs {
	u := &sdkinv.Universe{Provider: "aws", Pins: map[string]string{"aws-sdk-go-v2": "release-1"}, Candidates: []sdkinv.Candidate{
		cand("ec2", "ec2/instance", sdkinv.ClassResource, 0),
		cand("ec2", "ec2/volume", sdkinv.ClassResource, 0),
		cand("ec2", "ec2/instancetype", sdkinv.ClassCatalog, 0),
		cand("ec2", "ec2/accountattribute", sdkinv.ClassNonResource, 0),
		cand("kms", "kms/key", sdkinv.ClassResource, 0),
		cand("kms", "kms/grant", sdkinv.ClassResource, 1),
		cand("kms", "kms/alias", sdkinv.ClassResource, 0),
		cand("iam", "iam/accountpasswordpolicy", sdkinv.ClassAttribute, 0),
		cand("run", "run/gadgets", sdkinv.ClassResource, 0, "preview-only"),
		cand("s3", "s3/bucket", sdkinv.ClassResource, 0),
	}}
	emits := []TypeDecl{
		{Service: "ec2", DiscoType: "aws:ec2:instance"},
		{Service: "ec2", DiscoType: "aws:ec2:instance-detail"},
		{Service: "ec2", DiscoType: "aws:ec2:volume-status"},
		{Service: "kms", DiscoType: "aws:kms:key"},
		{Service: "kms", DiscoType: "aws:kms:grant"},
		{Service: "iam", DiscoType: "aws:iam:account-password-policy"},
		{Service: "s3", DiscoType: "aws:s3:bucket"},
		{Service: "entra", DiscoType: "aws:entra:user"},
		{Service: "foo", DiscoType: "aws:foo:bar"},
	}
	pairings := []pairing.Pairing{
		{Key: "ec2/instance", Kind: "emits", Types: []string{"aws:ec2:instance-detail", "aws:ec2:instance"}},
		{Key: "ec2/volume", Kind: "label"}, // a label with no SDK call proves nothing
		{Key: "kms/key", Kind: "emits", Types: []string{"aws:kms:key"}},
		{Key: "kms/alias", Kind: "sidecar"},
		{Key: "iam/accountpasswordpolicy", Kind: "emits", Types: []string{"aws:iam:account-password-policy"}},
		{Key: "", Kind: "other", Label: "ec2:DescribeVolumeStatus", Types: []string{"aws:ec2:volume-status"}},
	}
	unpaired := map[string]string{"aws:entra:user": "non-sdk", "aws:foo:bar": "unexplained", "aws:ec2:volume-status": "other-op:ec2:DescribeVolumeStatus"}
	return Inputs{Provider: "aws", Emits: emits, Universe: u, Pairings: pairings, Unpaired: unpaired, Paired: true}
}

func rowByKey(m Matrix, key, discoType string) (Row, bool) {
	for _, r := range m.Rows {
		if r.Key == key && (discoType == "" || r.DiscoType == discoType) {
			return r, true
		}
	}
	return Row{}, false
}

func TestBuildInventory_Buckets(t *testing.T) {
	m := BuildInventory(testInputs())
	want := map[string]struct {
		bucket    Bucket
		discoType string
		reason    string
	}{
		"ec2/instance":              {BucketCovered, "aws:ec2:instance", ""},
		"ec2/volume":                {BucketUncovered, "", ""},
		"ec2/instancetype":          {BucketExcluded, "", "catalog"},
		"ec2/accountattribute":      {BucketExcluded, "", "non-resource"},
		"kms/key":                   {BucketCovered, "aws:kms:key", ""},
		"kms/grant":                 {BucketCovered, "aws:kms:grant", ReasonMatchedByName},
		"kms/alias":                 {BucketCovered, "", ReasonSidecar},
		"iam/accountpasswordpolicy": {BucketAttribute, "aws:iam:account-password-policy", ""},
		"run/gadgets":               {BucketExcluded, "", "preview-only"},
		"s3/bucket":                 {BucketCovered, "aws:s3:bucket", ReasonMatchedByName},
	}
	for key, w := range want {
		r, ok := rowByKey(m, key, "")
		if !ok {
			t.Errorf("%s: no row", key)
			continue
		}
		if r.Bucket != w.bucket || r.DiscoType != w.discoType || r.Reason != w.reason {
			t.Errorf("%s = %s %q %q, want %s %q %q", key, r.Bucket, r.DiscoType, r.Reason, w.bucket, w.discoType, w.reason)
		}
	}
	if r, _ := rowByKey(m, "ec2/instance", ""); len(r.DiscoTypes) != 2 || r.DiscoTypes[0] != "aws:ec2:instance" || r.DiscoTypes[1] != "aws:ec2:instance-detail" {
		t.Errorf("instance discoTypes = %v", r.DiscoTypes)
	}
	if r, _ := rowByKey(m, "kms/key", ""); r.DiscoTypes != nil {
		t.Errorf("single-type row carries discoTypes %v", r.DiscoTypes)
	}
	if r, _ := rowByKey(m, "kms/grant", ""); r.Depth != 1 || r.Parent != "kms/key" || r.Scope != "region" || len(r.Ops) != 1 {
		t.Errorf("kms/grant row = %+v", r)
	}
	discoOnly := map[string]string{}
	for _, r := range m.Rows {
		if r.Bucket == BucketDiscoOnly {
			discoOnly[r.DiscoType] = r.Reason
		}
	}
	if len(discoOnly) != 3 || discoOnly["aws:entra:user"] != "explained: non-sdk" || discoOnly["aws:foo:bar"] != ReasonUnexplained ||
		discoOnly["aws:ec2:volume-status"] != "explained: other-op:ec2:DescribeVolumeStatus" {
		t.Errorf("disco-only = %v", discoOnly)
	}
}

func TestBuildInventory_Summary(t *testing.T) {
	m := BuildInventory(testInputs())
	s := m.Summary
	if s.Covered != 5 || s.Uncovered != 1 || s.Attribute != 1 || s.Excluded != 3 || s.DiscoOnly != 3 || s.Unexplained != 1 {
		t.Errorf("summary = %+v", s)
	}
	if s.Percent < 83.3 || s.Percent > 83.4 {
		t.Errorf("percent = %v", s.Percent)
	}
	if len(s.ByDepth) != 2 || s.ByDepth[0].Depth != 0 || s.ByDepth[0].Covered != 4 || s.ByDepth[0].Uncovered != 1 || s.ByDepth[1].Percent != 100 {
		t.Errorf("byDepth = %+v", s.ByDepth)
	}
	// Largest gap first, then alphabetical.
	if len(m.Services) != 3 || m.Services[0].Service != "ec2" || m.Services[1].Service != "kms" || m.Services[2].Service != "s3" {
		t.Errorf("services = %+v", m.Services)
	}
	if m.Services[1].Covered != 3 || m.Services[1].Percent != 100 {
		t.Errorf("kms summary = %+v", m.Services[1])
	}
	if !m.Pairing || m.Pins["aws-sdk-go-v2"] != "release-1" {
		t.Errorf("matrix header = pairing %v pins %v", m.Pairing, m.Pins)
	}
}

func TestBuildInventory_NoPairing(t *testing.T) {
	in := testInputs()
	in.Pairings, in.Unpaired, in.Paired = nil, nil, false
	m := BuildInventory(in)
	if m.Pairing {
		t.Error("pairing reported available")
	}
	// Name matching still works; everything else is uncovered, and no
	// disco-only row can be called unexplained.
	if r, _ := rowByKey(m, "ec2/instance", ""); r.Bucket != BucketCovered || r.Reason != ReasonMatchedByName {
		t.Errorf("instance = %+v", r)
	}
	if r, _ := rowByKey(m, "kms/alias", ""); r.Bucket != BucketUncovered {
		t.Errorf("alias = %+v", r)
	}
	if m.Summary.Unexplained != 0 {
		t.Errorf("unexplained = %d", m.Summary.Unexplained)
	}
	for _, r := range m.Rows {
		if r.Bucket == BucketDiscoOnly && r.Reason != ReasonPairingUnavailable {
			t.Errorf("disco-only reason = %q", r.Reason)
		}
	}
}

func TestFilter(t *testing.T) {
	m := BuildInventory(testInputs())
	gaps := Filter(m.Rows, "gaps", nil)
	if len(gaps) != 2 || gaps[0].Key != "ec2/volume" || gaps[1].DiscoType != "aws:foo:bar" {
		t.Errorf("gaps = %+v", gaps)
	}
	if got := Filter(m.Rows, "covered", []string{"KMS"}); len(got) != 3 {
		t.Errorf("covered kms = %+v", got)
	}
	if got := Filter(m.Rows, "all", nil); len(got) != len(m.Rows) {
		t.Error("all dropped rows")
	}
	// Filtering never changes the summary: it was computed first.
	if m.Summary.Covered != 5 {
		t.Error("summary changed")
	}
}

type fakeCrossChecker struct{}

func (fakeCrossChecker) CrossCheck(context.Context, FetchOptions) ([]UpstreamType, error) {
	return nil, nil
}
func (fakeCrossChecker) RegistryKey(c sdkinv.Candidate) string { return c.Key }
func (fakeCrossChecker) CanonicalKey(k string) string          { return strings.ToLower(k) }

func TestCrossCheck(t *testing.T) {
	in := testInputs()
	m := BuildInventory(in)
	CrossCheck(&m, in.Universe, []UpstreamType{
		{Key: "EC2/Instance", Service: "ec2"},
		{Key: "ec2/instance", Service: "ec2"}, // same identity: one row at most
		{Key: "ec2/phantom", Service: "ec2"},
		{Key: "ec2/instancetype", Service: "ec2"}, // catalog candidate: known to the universe, so not drift
		{Key: "zzz/thing", Service: "zzz"},        // service outside the universe: excluded by rule, not drift
	}, fakeCrossChecker{})
	drift := map[string]string{}
	for _, r := range m.Rows {
		if r.Bucket == BucketRegistryDrift {
			drift[r.Key] = r.Reason
		}
	}
	if drift["ec2/phantom"] != ReasonRegistryOnly {
		t.Errorf("registry-only row missing: %v", drift)
	}
	for _, k := range []string{"ec2/instancetype", "zzz/thing"} {
		if _, ok := drift[k]; ok {
			t.Errorf("%s reported as drift", k)
		}
	}
	if _, ok := drift["ec2/instance"]; ok {
		t.Error("matched registry key reported as drift")
	}
	if drift["ec2/volume"] != ReasonCandidateOnly || drift["kms/grant"] != ReasonCandidateOnly {
		t.Errorf("candidate-only rows missing: %v", drift)
	}
	if _, ok := drift["run/gadgets"]; !ok {
		t.Error("preview resource candidate not cross-checked")
	}
	if got := Filter(m.Rows, "registry-drift", nil); len(got) != len(drift) {
		t.Errorf("filter registry-drift = %d rows, want %d", len(got), len(drift))
	}
}

func TestRenderers(t *testing.T) {
	m := BuildInventory(testInputs())
	var md bytes.Buffer
	if err := RenderMarkdown(&md, []Matrix{m}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## AWS", "**Coverage:** 83.3% (5/6 listable)", "depth0 80.0%", "depth1 100.0%", "Pins: aws-sdk-go-v2@release-1",
		"| ec2 | 1 | 1 | 50.0 |", "### Uncovered (listable, no scanner) (1)", "| ec2 | ec2/volume | 0 | region | sr-resource | ec2:Listvolume |", "| sr-resource | 5 | 1 | 83.3 |", "The denominator is every candidate", "### Disco-only", "explained: non-sdk",
	} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("markdown lacks %q:\n%s", want, md.String())
		}
	}
	var tb bytes.Buffer
	if err := RenderTable(&tb, []Matrix{m}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tb.String(), "aws: **Coverage:** 83.3%") || !strings.Contains(tb.String(), "PROVIDER  SERVICE  KEY") {
		t.Errorf("table:\n%s", tb.String())
	}
	var js bytes.Buffer
	if err := RenderJSON(&js, []Matrix{m}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"pairing": true`, `"percent": 83.33`, `"byDepth"`, `"discoType": "aws:ec2:instance"`, `"services"`} {
		if !strings.Contains(js.String(), want) {
			t.Errorf("json lacks %q", want)
		}
	}
}

func TestIdentities(t *testing.T) {
	cases := map[string]string{
		"azure:microsoft.compute:virtual-machines:extensions": "microsoftcompute/virtualmachin/extension",
		"gcp:compute:region-disk":                             "compute/regiondisk",
		"aws:s3:bucket-policy":                                "s3/bucketpolicy",
		"gcp:storage:anywhere-cache":                          "storage/anywherecach",
	}
	for typ, want := range cases {
		if got := typeIdents(typ); len(got) != 1 || got[0] != want {
			t.Errorf("typeIdents(%s) = %v", typ, got)
		}
	}
	c := sdkinv.Candidate{Service: "microsoft.compute", Key: "microsoft.compute/virtualmachines/extensions"}
	if got := candidateIdent(c); got != "microsoftcompute/virtualmachin/extension" {
		t.Errorf("candidateIdent = %s", got)
	}
	if got := candidateIdent(sdkinv.Candidate{Service: "storage", Key: "storage/anywherecaches"}); got != "storage/anywherecach" {
		t.Errorf("candidateIdent caches = %s", got)
	}
	// The displayed type is the one whose identity matches the key, not the
	// alphabetically first reachable type.
	paired := map[string]bool{"aws:ec2:instance-detail": true, "aws:ec2:instance": true, "aws:ec2:address": true}
	if got := bestType(paired, sdkinv.Candidate{Service: "ec2", Key: "ec2/instance"}); got != "aws:ec2:instance" {
		t.Errorf("bestType = %s", got)
	}
	if got := bestType(map[string]bool{"gcp:cloudkms:crypto-key": true, "gcp:cloudkms:key-ring": true}, sdkinv.Candidate{Service: "cloudkms", Key: "cloudkms/keyrings/cryptokeys"}); got != "gcp:cloudkms:crypto-key" {
		t.Errorf("bestType leaf = %s", got)
	}
}

func TestTypeRefs(t *testing.T) {
	in := testInputs()
	for i := range in.Universe.Candidates {
		switch in.Universe.Candidates[i].Key {
		case "ec2/instance":
			in.Universe.Candidates[i].Refs = []string{"SubnetId", "VpcId"}
		case "kms/key":
			in.Universe.Candidates[i].Refs = []string{"Arn"}
		}
	}
	refs := TypeRefs(BuildInventory(in))
	// The row's primary type inherits the candidate's refs; the other types
	// the same listing stores do not (a dispatcher's derived pairing would
	// otherwise spread one element's fields across a whole service).
	if got := refs["aws:ec2:instance"]; !slices.Equal(got, []string{"SubnetId", "VpcId"}) {
		t.Errorf("instance refs = %v", got)
	}
	if got := refs["aws:ec2:instance-detail"]; len(got) > 0 {
		t.Errorf("instance-detail refs = %v, want none", got)
	}
	if got := refs["aws:kms:key"]; !slices.Equal(got, []string{"Arn"}) {
		t.Errorf("kms key refs = %v", got)
	}
	// A paired type whose element names nothing, and an unpaired type, have none.
	for _, typ := range []string{"aws:kms:grant", "aws:foo:bar", "aws:s3:bucket"} {
		if got, ok := refs[typ]; ok && len(got) > 0 {
			t.Errorf("%s refs = %v, want none", typ, got)
		}
	}
}

// TestBuildInventory_PairedButEmpty: a scanner tree that parses and anchors
// nothing is a total pairing collapse, not "no scanner source". Reporting it
// as the latter stamped every disco-only row `pairing-unavailable`, which
// left Summary.Unexplained at 0 and made --check-strict vacuous.
func TestBuildInventory_PairedButEmpty(t *testing.T) {
	in := testInputs()
	in.Pairings, in.Unpaired = nil, nil
	in.Paired = true
	m := BuildInventory(in)
	if !m.Pairing {
		t.Error("pairing reported unavailable")
	}
	if m.Summary.Unexplained == 0 {
		t.Error("no type reported unexplained")
	}
	for _, r := range m.Rows {
		if r.Bucket == BucketDiscoOnly && r.Reason == ReasonPairingUnavailable {
			t.Errorf("row stamped pairing-unavailable: %+v", r)
		}
	}
}

// TestBestType_MultiTypeHasNoWinner: with several types paired and neither
// the ident nor the leaf matching, naming one is a coin toss — it showed the
// diagnostic-settings dispatcher as "azure:microsoft.apimanagement:service".
// A lone paired type is still the answer.
func TestBestType_MultiTypeHasNoWinner(t *testing.T) {
	c := sdkinv.Candidate{Service: "route53", Key: "route53/tag"}
	paired := map[string]bool{"aws:route53:cidr-collection": true, "aws:route53:hosted-zone": true}
	if got := bestType(paired, c); got != "" {
		t.Errorf("bestType = %q, want empty", got)
	}
	if got := bestType(map[string]bool{"aws:route53:hosted-zone": true}, c); got != "aws:route53:hosted-zone" {
		t.Errorf("single paired type = %q", got)
	}
}

// TestBuildInventory_MultiTypeRowStaysCovered: clearing the arbitrary display
// type must not cost the row its bucket — the pairing is what covers it.
func TestBuildInventory_MultiTypeRowStaysCovered(t *testing.T) {
	in := testInputs()
	in.Pairings = append(in.Pairings, pairing.Pairing{
		Key: "s3/bucket", Kind: "emits", Types: []string{"aws:kms:grant", "aws:foo:bar"},
	})
	m := BuildInventory(in)
	r, ok := rowByKey(m, "s3/bucket", "")
	if !ok {
		t.Fatal("no s3/bucket row")
	}
	if r.Bucket != BucketCovered || r.DiscoType != "" || r.Reason != ReasonMultiType {
		t.Errorf("row = %+v, want covered/empty type/multi-type", r)
	}
	if !slices.Equal(r.DiscoTypes, []string{"aws:foo:bar", "aws:kms:grant"}) {
		t.Errorf("discoTypes = %v", r.DiscoTypes)
	}
}

// TestTypeRefs_FoldsLeafMatchingSecondaryTypes: the five aws:docdb:* orphans
// share rds/dbinstance's leaf and were starved of its refs, while a
// dispatcher's unrelated types must still inherit nothing.
func TestTypeRefs_FoldsLeafMatchingSecondaryTypes(t *testing.T) {
	in := testInputs()
	in.Universe.Candidates = append(in.Universe.Candidates,
		sdkinv.Candidate{Service: "rds", Key: "rds/dbinstances", Class: sdkinv.ClassResource, Refs: []string{"KmsKeyId"}})
	in.Emits = append(in.Emits,
		TypeDecl{Service: "rds", DiscoType: "aws:rds:db-instance"},
		TypeDecl{Service: "docdb", DiscoType: "aws:docdb:db-instance"},
		TypeDecl{Service: "amplify", DiscoType: "aws:amplify:app"})
	in.Pairings = append(in.Pairings, pairing.Pairing{
		Key: "rds/dbinstances", Kind: "emits",
		Types: []string{"aws:rds:db-instance", "aws:docdb:db-instance", "aws:amplify:app"},
	})
	refs := TypeRefs(BuildInventory(in))
	for _, typ := range []string{"aws:rds:db-instance", "aws:docdb:db-instance"} {
		if got := refs[typ]; !slices.Equal(got, []string{"KmsKeyId"}) {
			t.Errorf("%s refs = %v, want [KmsKeyId]", typ, got)
		}
	}
	if got := refs["aws:amplify:app"]; len(got) > 0 {
		t.Errorf("foreign type inherited refs %v", got)
	}
}

// TestBuildInventory_NameMatchYieldsToUnexplained: the name match set
// accounted[t], which suppressed the type's disco-only row and therefore
// Summary.Unexplained — the only number --check-strict exits on. A scanner
// that loses its listing call while keeping its Type constant stayed covered
// and kept both gates green.
func TestBuildInventory_NameMatchYieldsToUnexplained(t *testing.T) {
	in := testInputs()
	in.Unpaired["aws:s3:bucket"] = ReasonUnexplained
	m := BuildInventory(in)
	if r, _ := rowByKey(m, "s3/bucket", ""); r.Bucket != BucketUncovered || r.DiscoType != "" {
		t.Errorf("s3/bucket = %s %q, want uncovered with no type", r.Bucket, r.DiscoType)
	}
	if m.Summary.Unexplained != 2 { // aws:foo:bar was already unexplained
		t.Errorf("unexplained = %d, want 2", m.Summary.Unexplained)
	}
	// An other-op or skew explanation still name-matches.
	in.Unpaired["aws:s3:bucket"] = "other-op:s3:HeadBucket"
	if r, _ := rowByKey(BuildInventory(in), "s3/bucket", ""); r.Reason != ReasonMatchedByName {
		t.Errorf("explained type = %q, want matched-by-name", r.Reason)
	}
}

// TestBuildInventory_ExcludedKeepsScannerEvidence: the class rule wins the
// bucket, but discarding the pairing hid 93 rows a scanner provably lists —
// the worklist for fixing the class rules.
func TestBuildInventory_ExcludedKeepsScannerEvidence(t *testing.T) {
	in := testInputs()
	in.Emits = append(in.Emits, TypeDecl{Service: "ec2", DiscoType: "aws:ec2:instance-type"})
	in.Pairings = append(in.Pairings, pairing.Pairing{
		Key: "ec2/instancetype", Kind: "emits", Types: []string{"aws:ec2:instance-type"},
	})
	m := BuildInventory(in)
	r, ok := rowByKey(m, "ec2/instancetype", "")
	if !ok {
		t.Fatal("no ec2/instancetype row")
	}
	if r.Bucket != BucketExcluded || r.Reason != "catalog" || r.DiscoType != "aws:ec2:instance-type" {
		t.Errorf("row = %+v", r)
	}
	if !slices.Contains(r.Signals, SignalScannerLists) {
		t.Errorf("signals = %v, want %s", r.Signals, SignalScannerLists)
	}
	if got := Filter(m.Rows, FilterScannerLists, nil); len(got) != 1 || got[0].Key != "ec2/instancetype" {
		t.Errorf("scanner-lists filter = %v", got)
	}
}

// TestBuildInventory_OpsDedupe: sibling models and per-version GCP documents
// repeat a label, and 930 rows rendered a duplicated ops cell.
func TestBuildInventory_OpsDedupe(t *testing.T) {
	in := testInputs()
	for i := range in.Universe.Candidates {
		if in.Universe.Candidates[i].Key == "kms/key" {
			in.Universe.Candidates[i].Ops = []sdkinv.Operation{
				{Label: "kms:ListKeys"}, {Label: "kms:ListKeys"}, {Label: "kms:DescribeKey"},
			}
		}
	}
	r, _ := rowByKey(BuildInventory(in), "kms/key", "")
	if !slices.Equal(r.Ops, []string{"kms:ListKeys", "kms:DescribeKey"}) {
		t.Errorf("ops = %v", r.Ops)
	}
}
