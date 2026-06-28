package coverage

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// RenderMarkdown writes a per-provider markdown matrix (covered / uncovered /
// uncatalogued / upstream-missing). Suitable for `disco coverage
// -o markdown` piped into docs/coverage.md or pasted into the README.
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
	if _, err := fmt.Fprintf(w, "## %s\n\n", strings.ToUpper(m.Provider)); err != nil {
		return err
	}

	covered, uncovered, notScannable, uncatalogued, missing := splitByBucket(m.Rows)

	counts := fmt.Sprintf("**Covered:** %d &nbsp;·&nbsp; **Uncovered:** %d &nbsp;·&nbsp; **Not-scannable:** %d &nbsp;·&nbsp; **Uncatalogued:** %d &nbsp;·&nbsp; **Upstream-missing:** %d\n\n",
		len(covered), len(uncovered), len(notScannable), len(uncatalogued), len(missing))
	if _, err := io.WriteString(w, counts); err != nil {
		return err
	}

	if len(covered) > 0 {
		if err := mdSection(w, "Covered", []string{"Service", "Disco type", "Upstream key"}, covered, func(r Row) []string {
			return []string{r.Service, r.DiscoType, r.UpstreamKey}
		}); err != nil {
			return err
		}
	}
	if len(uncovered) > 0 {
		if err := mdSection(w, "Uncovered (in upstream registry, no disco scanner)",
			[]string{"Service", "Upstream key"}, uncovered, func(r Row) []string {
				return []string{r.Service, r.UpstreamKey}
			}); err != nil {
			return err
		}
	}
	if len(notScannable) > 0 {
		if err := mdSection(w, "Not-scannable (deliberately unscanned — sub-resource, ephemeral, no SDK, or duplicate)",
			[]string{"Service", "Upstream key", "Reason"}, notScannable, func(r Row) []string {
				return []string{r.Service, r.UpstreamKey, r.Reason}
			}); err != nil {
			return err
		}
	}
	if len(uncatalogued) > 0 {
		if err := mdSection(w, "Uncatalogued (disco scans it; no upstream registry catalogs it)",
			[]string{"Service", "Disco type"}, uncatalogued, func(r Row) []string {
				return []string{r.Service, r.DiscoType}
			}); err != nil {
			return err
		}
	}
	if len(missing) > 0 {
		if err := mdSection(w, "Upstream-missing (drift signal — disco emits but upstream registry does not list)",
			[]string{"Service", "Disco type", "Expected upstream key"}, missing, func(r Row) []string {
				return []string{r.Service, r.DiscoType, r.UpstreamKey}
			}); err != nil {
			return err
		}
	}
	return nil
}

func splitByBucket(rows []Row) (covered, uncovered, notScannable, uncatalogued, missing []Row) {
	for _, r := range rows {
		switch r.Bucket {
		case BucketCovered:
			covered = append(covered, r)
		case BucketUncovered:
			uncovered = append(uncovered, r)
		case BucketNotScannable:
			notScannable = append(notScannable, r)
		case BucketUncatalogued:
			uncatalogued = append(uncatalogued, r)
		case BucketUpstreamMissing:
			missing = append(missing, r)
		}
	}
	return
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
	// Group by service, then sorted within group.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Service != rows[j].Service {
			return rows[i].Service < rows[j].Service
		}
		ki, kj := rows[i].DiscoType, rows[j].DiscoType
		if ki == "" {
			ki = rows[i].UpstreamKey
		}
		if kj == "" {
			kj = rows[j].UpstreamKey
		}
		return ki < kj
	})
	for _, r := range rows {
		if _, err := fmt.Fprintln(w, "| "+strings.Join(cells(r), " | ")+" |"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return nil
}
