package azure

import (
	"bufio"
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/coverage"
	"github.com/icearp/disco-cli/internal/sdkinv"
)

// TestReconcileHandLists is the one-time report that lets Phase 5 delete
// azureAPITypeMap, the Uncatalogued flags and docs/azure-type-coverage.md: it
// classifies every entry against the SDK-derived inventory. Gated by
// DISCO_RECONCILE=1; run with -v to read the report. It never fails — the
// `*-orphan` and `ledger-absent` rows are triaged by hand.
func TestReconcileHandLists(t *testing.T) {
	if os.Getenv("DISCO_RECONCILE") == "" {
		t.Skip("set DISCO_RECONCILE=1")
	}
	in, err := coverage.InputsFromCache(context.Background(), sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}, "azure", CollectEmits(), ".")
	if errors.Is(err, sdkinv.ErrNotFetched) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	m := coverage.BuildInventory(in)
	report := map[string][]string{}
	add := func(category, line string) { report[category] = append(report[category], line) }

	state := map[string]string{}
	for _, r := range m.Rows {
		if r.DiscoType == "" {
			continue
		}
		s := string(r.Bucket)
		if r.Reason != "" {
			s += ": " + r.Reason
		}
		if prev, ok := state[r.DiscoType]; !ok || r.Bucket != coverage.BucketDiscoOnly && strings.HasPrefix(prev, "disco-only") {
			state[r.DiscoType] = s
		}
	}
	for upstream, typ := range azureAPITypeMap {
		if s := state[typ]; strings.HasPrefix(s, "disco-only") {
			add("alias-orphan", upstream+" = "+typ+" ("+s+")")
		} else {
			add("alias-redundant", upstream+" = "+typ+" ("+s+")")
		}
	}
	for _, d := range registeredDescriptors {
		if !d.Uncatalogued {
			continue
		}
		if s := state[d.Type]; s == "disco-only: "+coverage.ReasonUnexplained {
			add("uncatalogued-unexplained", d.Type)
		} else {
			add("uncatalogued-explained", d.Type+" ("+s+")")
		}
	}

	keys := map[string]bool{}
	for _, c := range in.Universe.Candidates {
		keys[c.Key] = true
	}
	for _, key := range ledgerIncludeKeys(t, "../../../docs/azure-type-coverage.md") {
		if keys[key] {
			add("ledger-present", key)
		} else {
			add("ledger-absent", key)
		}
	}

	cats := make([]string, 0, len(report))
	for c := range report {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		lines := report[c]
		sort.Strings(lines)
		t.Logf("== %s (%d)\n%s", c, len(lines), strings.Join(lines, "\n"))
	}
}

// ledgerIncludeKeys reads the ARM keys (first table cell) of the ledger's
// "## INCLUDE" section.
func ledgerIncludeKeys(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var keys []string
	inSection := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			inSection = strings.HasPrefix(line, "## INCLUDE")
			continue
		}
		if !inSection || !strings.HasPrefix(line, "| microsoft.") {
			continue
		}
		cell := strings.TrimSpace(strings.SplitN(line, "|", 3)[1])
		keys = append(keys, strings.ToLower(cell))
	}
	return keys
}
