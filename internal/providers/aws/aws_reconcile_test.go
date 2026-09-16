package aws

import (
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
// hand-maintained skip list, descriptor aliases and Uncatalogued flags: it
// classifies every entry against the SDK-derived inventory. Gated by
// DISCO_RECONCILE=1; run with -v to read the report. It never fails — the
// `*-contradicted` and `*-orphan` rows are triaged by hand.
func TestReconcileHandLists(t *testing.T) {
	if os.Getenv("DISCO_RECONCILE") == "" {
		t.Skip("set DISCO_RECONCILE=1")
	}
	in, err := coverage.InputsFromCache(context.Background(), sdkinv.Cache{Root: sdkinv.DefaultCacheRoot()}, "aws", CollectEmits(), ".")
	if errors.Is(err, sdkinv.ErrNotFetched) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	m := coverage.BuildInventory(in)
	p := coverageProvider{}
	report := map[string][]string{}
	add := func(category, line string) { report[category] = append(report[category], line) }

	// Skips: a skipped registry key against the candidates sharing its identity.
	byIdentity := map[string][]coverage.Row{}
	services := map[string]bool{}
	for _, c := range in.Universe.Candidates {
		services[canonService(c.Service)] = true
	}
	for _, r := range m.Rows {
		if r.Key != "" && r.Bucket != coverage.BucketDiscoOnly {
			id := canonService(r.Service) + "::" + canonResource(r.Key[strings.LastIndex(r.Key, "/")+1:])
			byIdentity[id] = append(byIdentity[id], r)
		}
	}
	for key, reason := range p.Skips() {
		id := p.CanonicalKey(key)
		rows := byIdentity[id]
		svc, _, _ := strings.Cut(id, "::")
		line := key + " (" + reason + ")"
		switch {
		case len(rows) == 0 && !services[svc]:
			add("skip-unmatched", line)
		case len(rows) == 0:
			add("skip-confirmed", line)
		default:
			r := rows[0]
			line += " -> " + r.Key + " " + string(r.Bucket)
			switch {
			case r.Bucket == coverage.BucketUncovered && r.Depth > 0:
				add("skip-is-child", line)
			case r.Bucket == coverage.BucketUncovered:
				add("skip-contradicted", line)
			case r.Bucket == coverage.BucketCovered:
				add("skip-covered", line)
			default:
				add("skip-confirmed", line)
			}
		}
	}

	state := typeStates(m)
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
	printReport(t, report)
}

// typeStates maps every emitted type to how the inventory accounts for it.
func typeStates(m coverage.Matrix) map[string]string {
	out := map[string]string{}
	for _, r := range m.Rows {
		if r.DiscoType == "" {
			continue
		}
		s := string(r.Bucket)
		if r.Reason != "" {
			s += ": " + r.Reason
		}
		if prev, ok := out[r.DiscoType]; !ok || r.Bucket != coverage.BucketDiscoOnly && strings.HasPrefix(prev, "disco-only") {
			out[r.DiscoType] = s
		}
	}
	return out
}

func printReport(t *testing.T, report map[string][]string) {
	t.Helper()
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
