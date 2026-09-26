package coverage

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func baselineMatrix(pins map[string]string, covered, uncovered, unexplained []string) Matrix {
	m := Matrix{Provider: "aws", Pins: pins, Pairing: true}
	for _, k := range covered {
		m.Rows = append(m.Rows, Row{Key: k, Bucket: BucketCovered})
	}
	for _, k := range uncovered {
		m.Rows = append(m.Rows, Row{Key: k, Bucket: BucketUncovered})
	}
	for _, t := range unexplained {
		m.Rows = append(m.Rows, Row{DiscoType: t, Bucket: BucketDiscoOnly, Reason: ReasonUnexplained})
	}
	m.Summary.Covered, m.Summary.Uncovered = len(covered), len(uncovered)
	if n := len(covered) + len(uncovered); n > 0 {
		m.Summary.Percent = 100 * float64(len(covered)) / float64(n)
	}
	return m
}

func driftKinds(ds []Drift) map[string][]string {
	out := map[string][]string{}
	for _, d := range ds {
		out[d.Kind] = append(out[d.Kind], d.Key)
	}
	return out
}

// TestCompareBaseline pins the ratchet: a regression, a percent drop under
// the same pins and a new unexplained type are fatal; growth under a pin
// bump only reports.
func TestCompareBaseline(t *testing.T) {
	pins := map[string]string{"sdk": "v1"}
	base := NewBaseline([]Matrix{baselineMatrix(pins, []string{"ec2/instance", "s3/bucket"}, []string{"ec2/fleet"}, []string{"aws:x:y"})})

	t.Run("identical is clean", func(t *testing.T) {
		if ds := CompareBaseline(base, []Matrix{baselineMatrix(pins, []string{"ec2/instance", "s3/bucket"}, []string{"ec2/fleet"}, []string{"aws:x:y"})}); len(ds) != 0 {
			t.Errorf("drifts = %+v", ds)
		}
	})
	t.Run("regression and percent drop are fatal", func(t *testing.T) {
		ds := CompareBaseline(base, []Matrix{baselineMatrix(pins, []string{"ec2/instance"}, []string{"ec2/fleet", "s3/bucket"}, []string{"aws:x:y"})})
		kinds := driftKinds(ds)
		if !reflect.DeepEqual(kinds[DriftRegressed], []string{"s3/bucket"}) || len(kinds[DriftPercentDrop]) != 1 {
			t.Errorf("drifts = %+v", ds)
		}
		if !ds[0].Fatal() {
			t.Error("regression must be fatal")
		}
	})
	t.Run("new unexplained is fatal", func(t *testing.T) {
		ds := CompareBaseline(base, []Matrix{baselineMatrix(pins, []string{"ec2/instance", "s3/bucket"}, []string{"ec2/fleet"}, []string{"aws:x:y", "aws:new:type"})})
		if kinds := driftKinds(ds); !reflect.DeepEqual(kinds[DriftNewUnexplained], []string{"aws:new:type"}) {
			t.Errorf("drifts = %+v", ds)
		}
	})
	t.Run("pin bump growth only reports", func(t *testing.T) {
		newPins := map[string]string{"sdk": "v2"}
		ds := CompareBaseline(base, []Matrix{baselineMatrix(newPins, []string{"ec2/instance", "s3/bucket"}, []string{"ec2/fleet", "ec2/newthing", "ec2/other"}, []string{"aws:x:y"})})
		for _, d := range ds {
			if d.Fatal() {
				t.Errorf("fatal drift under a pin bump: %+v", d)
			}
		}
		kinds := driftKinds(ds)
		if len(kinds[DriftPinsChanged]) != 1 || !reflect.DeepEqual(kinds[DriftNewSince], []string{"ec2/newthing", "ec2/other"}) {
			t.Errorf("drifts = %+v", ds)
		}
	})
	t.Run("regression under a pin bump is still fatal", func(t *testing.T) {
		ds := CompareBaseline(base, []Matrix{baselineMatrix(map[string]string{"sdk": "v2"}, []string{"ec2/instance"}, []string{"ec2/fleet", "s3/bucket"}, []string{"aws:x:y"})})
		if kinds := driftKinds(ds); !reflect.DeepEqual(kinds[DriftRegressed], []string{"s3/bucket"}) || len(kinds[DriftPercentDrop]) != 0 {
			t.Errorf("drifts = %+v", ds)
		}
	})
	t.Run("covered key gone is fatal under the same pins, reported under new ones", func(t *testing.T) {
		ds := CompareBaseline(base, []Matrix{baselineMatrix(pins, []string{"ec2/instance"}, []string{"ec2/fleet"}, []string{"aws:x:y"})})
		if kinds := driftKinds(ds); !reflect.DeepEqual(kinds[DriftRegressed], []string{"s3/bucket"}) {
			t.Errorf("same pins: drifts = %+v", ds)
		}
		ds = CompareBaseline(base, []Matrix{baselineMatrix(map[string]string{"sdk": "v2"}, []string{"ec2/instance"}, []string{"ec2/fleet"}, []string{"aws:x:y"})})
		kinds := driftKinds(ds)
		if !reflect.DeepEqual(kinds[DriftGoneSince], []string{"s3/bucket"}) || len(kinds[DriftRegressed]) != 0 {
			t.Errorf("new pins: drifts = %+v", ds)
		}
	})
	t.Run("provider without baseline reports", func(t *testing.T) {
		m := baselineMatrix(pins, nil, nil, nil)
		m.Provider = "gcp"
		// Fatal: a partial --write-baseline used to leave a file with one
		// provider in it, after which the other two were unguarded and CI
		// stayed green.
		if ds := CompareBaseline(base, []Matrix{m}); len(ds) != 1 || ds[0].Kind != DriftNoBaseline || !ds[0].Fatal() {
			t.Errorf("drifts = %+v", ds)
		}
	})
	t.Run("pairing mode mismatch is fatal and stops the compare", func(t *testing.T) {
		m := baselineMatrix(pins, []string{"ec2/instance", "s3/bucket"}, []string{"ec2/fleet"}, []string{"aws:x:y"})
		m.Pairing = false
		ds := CompareBaseline(base, []Matrix{m})
		if len(ds) != 1 || ds[0].Kind != DriftPairingMode || !ds[0].Fatal() {
			t.Errorf("drifts = %+v", ds)
		}
	})
	t.Run("an uncovered key leaving the universe is reported", func(t *testing.T) {
		ds := CompareBaseline(base, []Matrix{baselineMatrix(pins, []string{"ec2/instance", "s3/bucket"}, nil, []string{"aws:x:y"})})
		kinds := driftKinds(ds)
		if !reflect.DeepEqual(kinds[DriftDenominatorCut], []string{"ec2/fleet"}) {
			t.Errorf("drifts = %+v", ds)
		}
		for _, d := range ds {
			if d.Fatal() {
				t.Errorf("shrinking the denominator must not be fatal: %+v", d)
			}
		}
	})
}

// TestMergeKeepsProvidersNotRewritten: `--providers aws --write-baseline`
// must not drop azure and gcp from the file.
func TestMergeKeepsProvidersNotRewritten(t *testing.T) {
	prev := Baseline{"aws": {Percent: 1}, "azure": {Percent: 2}, "gcp": {Percent: 3}}
	fresh := Baseline{"aws": {Percent: 9}}
	got := Merge(prev, fresh)
	if len(got) != 3 || got["aws"].Percent != 9 || got["azure"].Percent != 2 || got["gcp"].Percent != 3 {
		t.Errorf("merged = %+v", got)
	}
}

// TestBaselineRoundTrip: the file survives write → read unchanged.
func TestBaselineRoundTrip(t *testing.T) {
	b := NewBaseline([]Matrix{baselineMatrix(map[string]string{"sdk": "v1"}, []string{"s3/bucket", "ec2/instance"}, []string{"ec2/fleet"}, nil)})
	path := filepath.Join(t.TempDir(), "b.json")
	if err := WriteBaseline(path, b); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, b) {
		t.Errorf("round trip: got %+v want %+v", got, b)
	}
	if got["aws"].CoveredKeys[0] != "ec2/instance" {
		t.Errorf("keys not sorted: %v", got["aws"].CoveredKeys)
	}
	if _, err := ReadBaseline(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file must error")
	}
}

// TestBaselineBytesIgnoreInputOrder is the property `make gen-coverage`
// depends on: NewBaseline → WriteBaseline over the same matrices supplied in a
// different order, with rows in a different order, writes identical bytes,
// so the committed baseline diffs only when the numbers do.
func TestBaselineBytesIgnoreInputOrder(t *testing.T) {
	pins := map[string]string{"sdk": "v1", "sr": "x"}
	aws := baselineMatrix(pins, []string{"s3/bucket", "ec2/instance"}, []string{"kms/grant", "ec2/fleet"}, []string{"aws:x:z", "aws:x:y"})
	awsRev := baselineMatrix(pins, []string{"ec2/instance", "s3/bucket"}, []string{"ec2/fleet", "kms/grant"}, []string{"aws:x:y", "aws:x:z"})
	gcp := baselineMatrix(pins, []string{"compute/instance"}, nil, nil)
	gcp.Provider = "gcp"
	write := func(ms ...Matrix) []byte {
		path := filepath.Join(t.TempDir(), "b.json")
		if err := WriteBaseline(path, NewBaseline(ms)); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	if a, b := write(aws, gcp), write(gcp, awsRev); !bytes.Equal(a, b) {
		t.Errorf("baseline bytes depend on input order:\n%s\n---\n%s", a, b)
	}
}
