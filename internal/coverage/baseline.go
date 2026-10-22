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
	Pins map[string]string `json:"pins"`
	// Pairing records whether the scanner source was paired when the numbers
	// were taken. A name-matching-only run reports ~7 points less coverage on
	// AWS, so comparing the two modes measures the mode, not the scanners.
	Pairing       bool     `json:"pairing"`
	Percent       float64  `json:"percent"`
	Covered       int      `json:"covered"`
	Uncovered     int      `json:"uncovered"`
	CoveredKeys   []string `json:"coveredKeys"`
	UncoveredKeys []string `json:"uncoveredKeys"`
	Unexplained   []string `json:"unexplained"` // disco-only types with no explanation
}

// Baseline is the checked-in coverage record, keyed by provider.
type Baseline map[string]ProviderBaseline

// NewBaseline records unfiltered matrices; keys are sorted so the file is
// byte-stable across runs.
func NewBaseline(ms []Matrix) Baseline {
	b := Baseline{}
	for _, m := range ms {
		pb := ProviderBaseline{
			Pins: m.Pins, Pairing: m.Pairing, Percent: m.Summary.Percent,
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

// Merge overlays fresh onto prev, provider by provider. `--providers aws
// --write-baseline` must not drop the providers it did not run: the ratchet
// would silently stop guarding them.
func Merge(prev, fresh Baseline) Baseline {
	out := Baseline{}
	for p, pb := range prev {
		out[p] = pb
	}
	for p, pb := range fresh {
		out[p] = pb
	}
	return out
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
	DriftRegressed      = "regressed"           // fatal: a covered key is no longer covered
	DriftPercentDrop    = "percent-drop"        // fatal: same pins, lower percent
	DriftNewUnexplained = "new-unexplained"     // fatal: an emitted type lost its SDK pairing
	DriftPairingMode    = "pairing-mode"        // fatal: baseline and run disagree on whether pairing ran
	DriftNoBaseline     = "no-baseline"         // fatal: provider absent from the file, so nothing guards it
	DriftNewSince       = "new-since-baseline"  // a key the baseline never saw (pin bump or new scanner)
	DriftGoneSince      = "gone-since-baseline" // a covered key no longer covered or uncovered (pin bump)
	DriftDenominatorCut = "denominator-shrunk"  // an uncovered key left the denominator (rule change)
	DriftPinsChanged    = "pins-changed"        // the denominator moved; percent is not comparable
)

// Drift is one difference between the baseline and a fresh matrix.
type Drift struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Key      string `json:"key,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// Bucket and Reason are the key's row in the fresh matrix when a baseline
	// covered key left covered/uncovered but is still a candidate (now an
	// attribute, or excluded); empty when no row carries the key.
	Bucket Bucket `json:"bucket,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Fatal reports whether the drift is a regression rather than movement.
func (d Drift) Fatal() bool {
	switch d.Kind {
	case DriftRegressed, DriftPercentDrop, DriftNewUnexplained, DriftNoBaseline, DriftPairingMode:
		return true
	}
	return false
}

// movedIndex holds the candidate rows outside covered/uncovered by key.
type movedIndex map[string]Row

// annotate records the key's current bucket and reason on d when the key is
// still a candidate in another bucket.
func (mv movedIndex) annotate(d Drift) Drift {
	r, ok := mv[d.Key]
	if !ok {
		return d
	}
	d.Bucket, d.Reason = r.Bucket, r.Reason
	return d
}

// movedRows indexes the candidate rows outside covered/uncovered by key.
// Rows arrive sorted, so the first row of a key siblings share is stable.
func movedRows(rows []Row) movedIndex {
	out := movedIndex{}
	for _, r := range rows {
		if r.Bucket != BucketAttribute && r.Bucket != BucketExcluded {
			continue
		}
		if _, dup := out[r.Key]; !dup {
			out[r.Key] = r
		}
	}
	return out
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
			out = append(out, Drift{Provider: m.Provider, Kind: DriftNoBaseline, Detail: "nothing guards this provider; regenerate with `make gen-coverage`"})
			continue
		}
		cur := NewBaseline([]Matrix{m})[m.Provider]
		if pb.Pairing != cur.Pairing {
			out = append(out, Drift{Provider: m.Provider, Kind: DriftPairingMode, Detail: fmt.Sprintf("baseline pairing=%t, run pairing=%t", pb.Pairing, cur.Pairing)})
			continue
		}
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
		// A covered key that left covered/uncovered: either no longer a
		// candidate at all, or still one in another bucket (attribute, or
		// excluded by class or as retired). Under the same pins the extractor
		// or scanner lost it; under new pins the SDK may have moved it.
		current := map[string]bool{}
		for _, k := range append(cur.CoveredKeys, cur.UncoveredKeys...) {
			current[k] = true
		}
		moved := movedRows(m.Rows)
		for _, k := range pb.CoveredKeys {
			if current[k] {
				continue
			}
			d := moved.annotate(Drift{Provider: m.Provider, Kind: DriftGoneSince, Key: k})
			// BuildInventory puts any scanner listing ahead of the retired
			// exclusion, so a covered key now excluded as retired lost its
			// listing whatever the SDK did: fatal under any pins, like covered
			// to uncovered. Other moves (a class change, preview-only, which
			// outranks a listing) can come from the SDK alone.
			if samePins || d.Reason == ReasonRetired {
				d.Kind = DriftRegressed
			}
			if d.Kind == DriftRegressed && d.Bucket == "" {
				d.Detail = "no longer a candidate"
			}
			out = append(out, d)
		}
		// An uncovered key leaving the denominator (the universe, or for
		// attribute/excluded) raises the percent, so no other check here
		// fires. That is what a classifier rule change looks like, and it
		// must still be reviewed.
		for _, k := range pb.UncoveredKeys {
			if !current[k] {
				out = append(out, moved.annotate(Drift{Provider: m.Provider, Kind: DriftDenominatorCut, Key: k}))
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
