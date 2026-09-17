package coverage

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
)

// ProviderBaseline is one provider's accepted coverage: the pins the numbers
// were computed under, the headline, and the keys behind it so a regression
// names the candidate that slipped rather than only a percentage.
type ProviderBaseline struct {
	Pins          map[string]string `json:"pins"`
	Percent       float64           `json:"percent"`
	Covered       int               `json:"covered"`
	Uncovered     int               `json:"uncovered"`
	CoveredKeys   []string          `json:"coveredKeys"`
	UncoveredKeys []string          `json:"uncoveredKeys"`
	Unexplained   []string          `json:"unexplained"` // disco-only types with no explanation
}

// Baseline is the checked-in coverage record, keyed by provider.
type Baseline map[string]ProviderBaseline

// NewBaseline records unfiltered matrices; keys are sorted so the file is
// byte-stable across runs.
func NewBaseline(ms []Matrix) Baseline {
	b := Baseline{}
	for _, m := range ms {
		pb := ProviderBaseline{
			Pins: m.Pins, Percent: m.Summary.Percent,
			Covered: m.Summary.Covered, Uncovered: m.Summary.Uncovered,
			CoveredKeys: []string{}, UncoveredKeys: []string{}, Unexplained: []string{},
		}
		for _, r := range m.Rows {
			switch {
			case r.Bucket == BucketCovered:
				pb.CoveredKeys = append(pb.CoveredKeys, r.Key)
			case r.Bucket == BucketUncovered:
				pb.UncoveredKeys = append(pb.UncoveredKeys, r.Key)
			case r.Bucket == BucketDiscoOnly && r.Reason == ReasonUnexplained:
				pb.Unexplained = append(pb.Unexplained, r.DiscoType)
			}
		}
		sort.Strings(pb.CoveredKeys)
		sort.Strings(pb.UncoveredKeys)
		sort.Strings(pb.Unexplained)
		b[m.Provider] = pb
	}
	return b
}

// ReadBaseline loads a file written by WriteBaseline.
func ReadBaseline(path string) (Baseline, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b Baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// WriteBaseline writes the file with a trailing newline, indented for review.
func WriteBaseline(path string, b Baseline) error {
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// Drift kinds. Fatal kinds fail `--baseline`; the rest are reported so a
// reviewer sees the denominator move.
const (
	DriftRegressed      = "regressed"           // fatal: a covered key is now uncovered
	DriftPercentDrop    = "percent-drop"        // fatal: same pins, lower percent
	DriftNewUnexplained = "new-unexplained"     // fatal: an emitted type lost its SDK pairing
	DriftNewSince       = "new-since-baseline"  // a key the baseline never saw (pin bump or new scanner)
	DriftGoneSince      = "gone-since-baseline" // a baseline key the SDK no longer lists (pin bump)
	DriftPinsChanged    = "pins-changed"        // the denominator moved; percent is not comparable
	DriftNoBaseline     = "no-baseline"         // provider absent from the file
)

// Drift is one difference between the baseline and a fresh matrix.
type Drift struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Key      string `json:"key,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Fatal reports whether the drift is a regression rather than movement.
func (d Drift) Fatal() bool {
	switch d.Kind {
	case DriftRegressed, DriftPercentDrop, DriftNewUnexplained:
		return true
	}
	return false
}

// CompareBaseline diffs fresh unfiltered matrices against the baseline.
// Denominator growth under a pin bump is never fatal: the new keys are
// listed and the percent is not compared, because a bigger universe with
// the same scanners can only lower it. A previously covered key that is now
// uncovered is fatal under any pins — that is a scanner losing a listing.
func CompareBaseline(b Baseline, ms []Matrix) []Drift {
	var out []Drift
	for _, m := range ms {
		pb, ok := b[m.Provider]
		if !ok {
			out = append(out, Drift{Provider: m.Provider, Kind: DriftNoBaseline})
			continue
		}
		cur := NewBaseline([]Matrix{m})[m.Provider]
		samePins := mapsEqual(pb.Pins, cur.Pins)
		if !samePins {
			out = append(out, Drift{Provider: m.Provider, Kind: DriftPinsChanged, Detail: fmt.Sprintf("%v -> %v", pb.Pins, cur.Pins)})
		}
		known := map[string]bool{}
		for _, k := range pb.CoveredKeys {
			known[k] = true
		}
		for _, k := range pb.UncoveredKeys {
			known[k] = true
		}
		for _, k := range cur.UncoveredKeys {
			switch {
			case slices.Contains(pb.CoveredKeys, k):
				out = append(out, Drift{Provider: m.Provider, Kind: DriftRegressed, Key: k})
			case !known[k]:
				out = append(out, Drift{Provider: m.Provider, Kind: DriftNewSince, Key: k, Detail: "uncovered"})
			}
		}
		for _, k := range cur.CoveredKeys {
			if !known[k] {
				out = append(out, Drift{Provider: m.Provider, Kind: DriftNewSince, Key: k, Detail: "covered"})
			}
		}
		// A covered key that is no longer a candidate at all: under the same
		// pins the universe cannot have shrunk, so the extractor or scanner
		// lost it; under new pins the SDK retired it.
		current := map[string]bool{}
		for _, k := range append(cur.CoveredKeys, cur.UncoveredKeys...) {
			current[k] = true
		}
		for _, k := range pb.CoveredKeys {
			if current[k] {
				continue
			}
			if samePins {
				out = append(out, Drift{Provider: m.Provider, Kind: DriftRegressed, Key: k, Detail: "no longer a candidate"})
			} else {
				out = append(out, Drift{Provider: m.Provider, Kind: DriftGoneSince, Key: k})
			}
		}
		for _, t := range cur.Unexplained {
			if !slices.Contains(pb.Unexplained, t) {
				out = append(out, Drift{Provider: m.Provider, Kind: DriftNewUnexplained, Key: t})
			}
		}
		if samePins && cur.Percent < pb.Percent {
			out = append(out, Drift{Provider: m.Provider, Kind: DriftPercentDrop, Detail: fmt.Sprintf("%.1f%% -> %.1f%%", pb.Percent, cur.Percent)})
		}
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
