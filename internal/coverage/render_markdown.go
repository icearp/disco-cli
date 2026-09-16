package coverage

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// RenderMarkdown writes one section per provider: the headline percentage,
// the per-service table (largest gaps first) and the bucket tables.
// Suitable for `disco coverage services -o markdown > docs/coverage.md`.
func RenderMarkdown(w io.Writer, matrices []Matrix) error {
	for i, m := range matrices {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if err := renderMatrixMarkdown(w, m); err != nil {
			return err
		}
	}
	return nil
}

func renderMatrixMarkdown(w io.Writer, m Matrix) error {
	if _, err := fmt.Fprintf(w, "## %s\n\n%s\n\n", strings.ToUpper(m.Provider), Headline(m)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Pins: %s\n\n", pinsLine(m.Pins)); err != nil {
		return err
	}
	if len(m.Services) > 0 {
		if _, err := fmt.Fprintln(w, "| Service | Covered | Uncovered | % |\n|---|---|---|---|"); err != nil {
			return err
		}
		for _, s := range m.Services {
			if _, err := fmt.Fprintf(w, "| %s | %d | %d | %.1f |\n", s.Service, s.Covered, s.Uncovered, s.Percent); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	sections := []struct {
		bucket  Bucket
		title   string
		headers []string
		cells   func(Row) []string
	}{
		{BucketUncovered, "Uncovered (listable, no scanner)", []string{"Service", "Key", "Depth", "Scope", "Ops"}, func(r Row) []string {
			return []string{r.Service, r.Key, fmt.Sprint(r.Depth), r.Scope, strings.Join(r.Ops, ", ")}
		}},
		{BucketDiscoOnly, "Disco-only (emitted type with no candidate)", []string{"Service", "Disco type", "Reason"}, func(r Row) []string {
			return []string{r.Service, r.DiscoType, r.Reason}
		}},
		{BucketRegistryDrift, "Registry drift (--cross-check)", []string{"Service", "Key", "Reason"}, func(r Row) []string {
			return []string{r.Service, r.Key, r.Reason}
		}},
		{BucketCovered, "Covered", []string{"Service", "Key", "Disco type", "Depth"}, func(r Row) []string {
			return []string{r.Service, r.Key, r.DiscoType, fmt.Sprint(r.Depth)}
		}},
		{BucketAttribute, "Attributes (detail reads, not counted)", []string{"Service", "Key", "Disco type", "Ops"}, func(r Row) []string {
			return []string{r.Service, r.Key, r.DiscoType, strings.Join(r.Ops, ", ")}
		}},
		{BucketExcluded, "Excluded (catalog, non-resource, preview-only)", []string{"Service", "Key", "Reason"}, func(r Row) []string {
			return []string{r.Service, r.Key, r.Reason}
		}},
	}
	for _, s := range sections {
		var rows []Row
		for _, r := range m.Rows {
			if r.Bucket == s.bucket {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			continue
		}
		if err := mdSection(w, s.title, s.headers, rows, s.cells); err != nil {
			return err
		}
	}
	return nil
}

// Headline is the one-line summary printed above every report.
func Headline(m Matrix) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Coverage:** %.1f%% (%d/%d listable)", m.Summary.Percent, m.Summary.Covered, m.Summary.Covered+m.Summary.Uncovered)
	for _, d := range m.Summary.ByDepth {
		fmt.Fprintf(&b, " · depth%d %.1f%%", d.Depth, d.Percent)
	}
	fmt.Fprintf(&b, " · attribute %d · excluded %d · disco-only %d (%d unexplained)", m.Summary.Attribute, m.Summary.Excluded, m.Summary.DiscoOnly, m.Summary.Unexplained)
	if !m.Pairing {
		b.WriteString(" · pairing unavailable: name matching only")
	}
	return b.String()
}

func pinsLine(pins map[string]string) string {
	keys := make([]string, 0, len(pins))
	for k := range pins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"@"+pins[k])
	}
	return strings.Join(parts, ", ")
}

func mdSection(w io.Writer, title string, headers []string, rows []Row, cells func(Row) []string) error {
	if _, err := fmt.Fprintf(w, "### %s (%d)\n\n", title, len(rows)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| "+strings.Join(headers, " | ")+" |"); err != nil {
		return err
	}
	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = "---"
	}
	if _, err := fmt.Fprintln(w, "| "+strings.Join(sep, " | ")+" |"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintln(w, "| "+strings.Join(cells(r), " | ")+" |"); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}
