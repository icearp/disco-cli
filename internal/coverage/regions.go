package coverage

import "sort"

// DiffRegions categorises every region across the union of static and live
// lists. Output is sorted by region name for diff-stable rendering. Provider
// field on each row is left empty; the caller fills it in.
func DiffRegions(static, live []string) []RegionRow {
	s := make(map[string]struct{}, len(static))
	for _, r := range static {
		s[r] = struct{}{}
	}
	l := make(map[string]struct{}, len(live))
	for _, r := range live {
		l[r] = struct{}{}
	}
	union := make([]string, 0, len(s)+len(l))
	seen := make(map[string]struct{}, len(s)+len(l))
	for r := range s {
		if _, ok := seen[r]; !ok {
			seen[r] = struct{}{}
			union = append(union, r)
		}
	}
	for r := range l {
		if _, ok := seen[r]; !ok {
			seen[r] = struct{}{}
			union = append(union, r)
		}
	}
	sort.Strings(union)

	out := make([]RegionRow, 0, len(union))
	for _, r := range union {
		_, inS := s[r]
		_, inL := l[r]
		switch {
		case inS && inL:
			out = append(out, RegionRow{Region: r, Status: RegionCovered})
		case inS:
			out = append(out, RegionRow{Region: r, Status: RegionStale})
		default:
			out = append(out, RegionRow{Region: r, Status: RegionMissing})
		}
	}
	return out
}
