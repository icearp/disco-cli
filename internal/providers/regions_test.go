package providers_test

import (
	"slices"
	"testing"

	"github.com/icearp/disco-cli/internal/providers"

	// Blank-import every provider so registry is populated for the test.
	_ "github.com/icearp/disco-cli/internal/providers/aws"
	_ "github.com/icearp/disco-cli/internal/providers/azure"
	_ "github.com/icearp/disco-cli/internal/providers/gcp"
)

// Every registered provider must satisfy ServiceNamer and RegionNamer, and
// each must return a non-empty slice with no duplicates and no empty entries.
// Catches typo-driven drift in regions.go on first build.
func TestProvidersExposeServicesAndRegions(t *testing.T) {
	for _, s := range providers.All() {
		t.Run(s.Name(), func(t *testing.T) {
			sn, ok := s.(providers.ServiceNamer)
			if !ok {
				t.Fatalf("%s: missing ServiceNamer", s.Name())
			}
			rn, ok := s.(providers.RegionNamer)
			if !ok {
				t.Fatalf("%s: missing RegionNamer", s.Name())
			}
			assertNonEmptyDistinct(t, "services", sn.ServiceNames())
			assertNonEmptyDistinct(t, "regions", rn.RegionNames())
		})
	}
}

func assertNonEmptyDistinct(t *testing.T, label string, got []string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("%s: empty slice", label)
	}
	seen := make(map[string]struct{}, len(got))
	for _, v := range got {
		if v == "" {
			t.Fatalf("%s: empty entry", label)
		}
		if _, dup := seen[v]; dup {
			t.Fatalf("%s: duplicate %q", label, v)
		}
		seen[v] = struct{}{}
	}
}

// Mutating the returned slice must not affect the next call — RegionNames
// and ServiceNames document slice-clone semantics.
func TestRegionNamesCloneSemantics(t *testing.T) {
	for _, s := range providers.All() {
		rn, ok := s.(providers.RegionNamer)
		if !ok {
			continue
		}
		first := rn.RegionNames()
		if len(first) == 0 {
			continue
		}
		first[0] = "MUTATED"
		second := rn.RegionNames()
		if slices.Contains(second, "MUTATED") {
			t.Fatalf("%s: RegionNames not cloned (callers can mutate package list)", s.Name())
		}
	}
}
