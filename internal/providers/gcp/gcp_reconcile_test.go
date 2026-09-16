package gcp

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

// TestReconcileHandLists is the one-time report that lets Phase 5 delete the
// descriptor aliases, the Uncatalogued flags and docs/gcp-type-coverage.md:
// it classifies every entry against the SDK-derived inventory. Gated by
// DISCO_RECONCILE=1; run with -v to read the report. It never fails — the
// `*-orphan` and `ledger-absent` rows are triaged by hand.
func TestReconcileHandLists(t *testing.T) {
	if os.Getenv("DISCO_RECONCILE") == "" {
		t.Skip("set DISCO_RECONCILE=1")
	}
	in, err := coverage.InputsFromCache(context.Background(), sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}, "gcp", CollectEmits(), ".")
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
	for typ, upstream := range descriptorAliases() {
		if s := state[typ]; strings.HasPrefix(s, "disco-only") {
			add("alias-orphan", typ+" = "+upstream+" ("+s+")")
		} else {
			add("alias-redundant", typ+" = "+upstream+" ("+s+")")
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

	p := coverageProvider{}
	ids := map[string]bool{}
	for _, c := range in.Universe.Candidates {
		ids[p.RegistryKey(c)] = true
	}
	for _, id := range ledgerIncludeIDs(t, "../../../docs/gcp-type-coverage.md") {
		if ids[id] {
			add("ledger-present", id)
		} else {
			add("ledger-absent", id)
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

// ledgerIncludeIDs reads the ledger's two "## INCLUDE" sections into
// RegistryKey identities: the Compute Engine table lists bare PascalCase
// types ("RegionDisk"); the non-compute table prefixes the API and may list
// several types per row ("admin Building, Calendar, Feature").
func ledgerIncludeIDs(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var ids []string
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			section = ""
			if strings.HasPrefix(line, "## INCLUDE — Compute") {
				section = "compute"
			} else if strings.HasPrefix(line, "## INCLUDE") {
				section = "other"
			}
			continue
		}
		if section == "" || !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| Type") || strings.HasPrefix(line, "|---") {
			continue
		}
		cell := strings.TrimSpace(strings.SplitN(line, "|", 3)[1])
		api := "compute"
		names := cell
		if section == "other" {
			api, names, _ = strings.Cut(cell, " ")
		}
		for _, n := range strings.Split(names, ",") {
			n = strings.TrimSpace(n)
			if n == "" || strings.HasPrefix(n, "(") || strings.HasPrefix(n, "*") {
				continue
			}
			if i := strings.IndexAny(n, " ("); i > 0 {
				n = n[:i]
			}
			ids = append(ids, api+"/"+sdkinv.Ident(n))
		}
	}
	return ids
}
