// disco-scaffold is a forward-only dev tool: it reads a provider's live
// upstream catalog (via the already-wired coverage.Provider.Fetch), subtracts
// the types disco already scans, and emits restype.Descriptor-shaped stubs for
// the *uncovered* types so a new scanner is born in the unified shape.
//
// It never rewrites existing files. --write drops a self-contained
// <svc>_scanners.go into the provider package (refusing to clobber an existing
// one unless --force); without --write it prints the same content to stdout.
//
//	go run ./cmd/disco-scaffold gcp:artifactregistry
//	go run ./cmd/disco-scaffold aws:ec2 --regions us-east-1 --profile audit
//	go run ./cmd/disco-scaffold aws:qbusiness --write
//
// The SDK->store.Resource field mapping and the resolver edges stay human
// judgment — the stub scanner returns (0,0,nil) with a TODO. Every upstream
// type already covered is logged, so the tool can never silently skip one.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codeberg.org/icearp/disco/internal/coverage"
	_ "codeberg.org/icearp/disco/internal/providers/all" // register providers + coverage impls
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(argv []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("disco-scaffold", flag.ContinueOnError)
	fs.SetOutput(stderr)
	regions := fs.String("regions", "", "comma-separated regions for the upstream Fetch (AWS CloudFormation)")
	profile := fs.String("profile", "", "AWS profile for the upstream Fetch")
	subscription := fs.String("subscription", "", "Azure subscription id for the upstream Fetch")
	write := fs.Bool("write", false, "write <svc>_scanners.go into the provider package instead of stdout")
	force := fs.Bool("force", false, "with --write, overwrite an existing file")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: disco-scaffold [flags] <provider>:<service>")
		fmt.Fprintln(stderr, "  e.g. disco-scaffold gcp:artifactregistry")
		fs.PrintDefaults()
	}
	// Pull the <provider>:<service> token (the unique bare arg containing ':')
	// out of argv so flags may sit on either side of it — Go's flag package
	// otherwise stops parsing at the first positional.
	var target string
	rest := make([]string, 0, len(argv))
	for _, a := range argv {
		if target == "" && !strings.HasPrefix(a, "-") && strings.Contains(a, ":") {
			target = a
			continue
		}
		rest = append(rest, a)
	}
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if target == "" || fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	provName, service, ok := strings.Cut(target, ":")
	if !ok || provName == "" || service == "" {
		fmt.Fprintln(stderr, "argument must be <provider>:<service>, e.g. gcp:artifactregistry")
		return 2
	}
	prov, ok := coverage.Get(provName)
	if !ok {
		fmt.Fprintf(stderr, "unknown provider %q (have: %s)\n", provName, providerNames())
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	opts := coverage.FetchOptions{Profile: *profile, Subscription: *subscription}
	if *regions != "" {
		opts.Regions = strings.Split(*regions, ",")
	}
	upstream, err := prov.Fetch(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "fetch upstream catalog for %s: %v\n", provName, err)
		return 1
	}

	uncovered, covered := classifyService(prov, upstream, service)
	if len(uncovered) == 0 && len(covered) == 0 {
		fmt.Fprintf(stderr, "no upstream types found for service %q under provider %q — check the service segment\n", service, provName)
		return 1
	}
	// No silent caps: report every already-covered type so a run can never be
	// mistaken for "nothing to do" when it actually skipped covered rows.
	fmt.Fprintf(stderr, "%s:%s — %d uncovered, %d already covered (skipped)\n", provName, service, len(uncovered), len(covered))
	for _, r := range covered {
		fmt.Fprintf(stderr, "  skip (covered): %s\n", r.UpstreamKey)
	}
	if len(uncovered) == 0 {
		fmt.Fprintf(stderr, "service %q is fully covered — nothing to scaffold\n", service)
		return 0
	}

	src := genScaffold(provName, service, uncovered, prov)
	if !*write {
		fmt.Fprint(stdout, src)
		return 0
	}
	path := filepath.Join("internal", "providers", provName, service+"_scanners.go")
	if _, err := os.Stat(path); err == nil && !*force {
		fmt.Fprintf(stderr, "%s already exists — refusing to overwrite (pass --force)\n", path)
		return 1
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		fmt.Fprintf(stderr, "write %s: %v\n", path, err)
		return 1
	}
	fmt.Fprintf(stderr, "wrote %s (%d types) — fill the TODO scanner body\n", path, len(uncovered))
	return 0
}

// classifyService builds the coverage matrix and splits the requested service's
// rows into uncovered (a genuine gap) and covered (already scanned) buckets.
func classifyService(prov coverage.Provider, upstream []coverage.UpstreamType, service string) (uncovered, covered []coverage.Row) {
	var skips map[string]string
	var canonical func(string) string
	if sk, ok := prov.(coverage.Skipper); ok {
		skips = sk.Skips()
	}
	if ck, ok := prov.(coverage.CanonicalKeyer); ok {
		canonical = ck.CanonicalKey
	}
	m := coverage.Build(prov.Name(), prov.Emits(), prov.Aliases(), prov.AlgorithmicKey, upstream, skips, canonical)
	for _, r := range m.Rows {
		if r.Service != service {
			continue
		}
		switch r.Bucket {
		case coverage.BucketUncovered:
			uncovered = append(uncovered, r)
		case coverage.BucketCovered:
			covered = append(covered, r)
		}
	}
	sort.Slice(uncovered, func(i, j int) bool { return uncovered[i].UpstreamKey < uncovered[j].UpstreamKey })
	sort.Slice(covered, func(i, j int) bool { return covered[i].UpstreamKey < covered[j].UpstreamKey })
	return uncovered, covered
}

func providerNames() string {
	var names []string
	for _, p := range coverage.All() {
		names = append(names, p.Name())
	}
	return strings.Join(names, ", ")
}
