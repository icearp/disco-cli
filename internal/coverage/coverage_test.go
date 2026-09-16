package coverage

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/icearp/disco-cli/internal/sdkinv/pairing"
)

func cand(service, key string, class sdkinv.Class, depth int, signals ...string) sdkinv.Candidate {
	c := sdkinv.Candidate{
		Provider: "aws", Service: service, Key: key, Class: class, Depth: depth, Signals: signals,
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
	return Inputs{Provider: "aws", Emits: emits, Universe: u, Pairings: pairings, Unpaired: unpaired}
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
	in.Pairings, in.Unpaired = nil, nil
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
		"| ec2 | 1 | 1 | 50.0 |", "### Uncovered (listable, no scanner) (1)", "| ec2 | ec2/volume | 0 | region | ec2:Listvolume |", "### Disco-only", "explained: non-sdk",
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
