package regions_test

import (
	"slices"
	"testing"

	"github.com/icearp/disco-cli/regions"
	// Blank-import the aggregator so every provider's leaf package registers its
	// region list — the same wiring a standalone consumer relies on.
	_ "github.com/icearp/disco-cli/regions/all"
)

func TestFor(t *testing.T) {
	for _, p := range []string{"aws", "azure", "gcp"} {
		got := regions.For(p)
		if len(got) == 0 {
			t.Errorf("regions.For(%q) is empty", p)
		}
		if !slices.IsSorted(got) {
			t.Errorf("regions.For(%q) is not sorted: %v", p, got)
		}
	}
	if regions.For("unknown") != nil {
		t.Errorf("regions.For(unknown) = non-nil; want nil")
	}
	// Returned slice is a copy: mutating it must not affect the source.
	a := regions.For("aws")
	if len(a) > 0 {
		a[0] = "MUTATED"
		if slices.Contains(regions.For("aws"), "MUTATED") {
			t.Errorf("For returned an aliased slice; mutation leaked")
		}
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		provider, region string
		want             bool
	}{
		{"aws", "us-east-1", true},
		{"aws", "ap-southeast-6", true},
		{"aws", "us-gov-west-1", false}, // GovCloud excluded
		{"aws", "cn-north-1", false},    // China excluded
		{"aws", "us-esat-1", false},     // typo
		{"azure", "eastus", true},
		{"azure", "us-east-1", false},
		{"gcp", "us-central1", true},
		{"gcp", "us-central-1", false},
		{"unknown", "whatever", false},
	}
	for _, c := range cases {
		if got := regions.Valid(c.provider, c.region); got != c.want {
			t.Errorf("regions.Valid(%q, %q) = %v; want %v", c.provider, c.region, got, c.want)
		}
	}
}

// TestServiceAvailable covers the three-state contract. known is the whole
// point of the signature: callers must be able to tell "AWS does not offer it
// there" from "nobody registered an opinion", because the first justifies
// skipping the region and the second must not.
func TestServiceAvailable(t *testing.T) {
	cases := []struct {
		name         string
		service      string
		region       string
		known, avail bool
	}{
		{"registered and offered", "aws:cassandra", "us-east-1", true, true},
		{"registered and not offered", "aws:cassandra", "ap-northeast-3", true, false},
		{"unregistered service", "aws:zzz-not-real", "us-east-1", false, false},
		// azure and gcp have no region axis to describe, so they register no
		// table and every lookup must report no opinion rather than "not there".
		{"provider with no table", "azure:storage", "eastus", false, false},
		{"unqualified name", "cassandra", "us-east-1", false, false},
		{"empty provider", ":cassandra", "us-east-1", false, false},
		{"empty service", "", "us-east-1", false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			known, avail := regions.ServiceAvailable(c.service, c.region)
			if known != c.known || avail != c.avail {
				t.Errorf("regions.ServiceAvailable(%q, %q) = (%v, %v); want (%v, %v)",
					c.service, c.region, known, avail, c.known, c.avail)
			}
		})
	}
}

func TestServiceRegions(t *testing.T) {
	got := regions.ServiceRegions("aws:cassandra")
	if len(got) == 0 {
		t.Fatal("regions.ServiceRegions(aws:cassandra) is empty")
	}
	if !slices.IsSorted(got) {
		t.Errorf("regions.ServiceRegions(aws:cassandra) is not sorted: %v", got)
	}
	if regions.ServiceRegions("aws:zzz-not-real") != nil {
		t.Error("regions.ServiceRegions(unregistered) = non-nil; want nil")
	}
	// Returned slice is a copy, matching For.
	got[0] = "MUTATED"
	if slices.Contains(regions.ServiceRegions("aws:cassandra"), "MUTATED") {
		t.Error("ServiceRegions returned an aliased slice; mutation leaked")
	}
}

// TestRegisterServicesRejectsAMisprefixedKey pins the init-time panic. A key
// registered under the wrong provider is unreachable by lookup — ServiceAvailable
// derives the provider from the name — so the table would load cleanly and
// answer "no opinion" to every query about it, forever.
func TestRegisterServicesRejectsAMisprefixedKey(t *testing.T) {
	for _, c := range []struct {
		name     string
		provider string
		table    map[string][]string
	}{
		{"wrong provider prefix", "test", map[string][]string{"aws:thing": {"us-east-1"}}},
		{"no prefix at all", "test", map[string][]string{"thing": {"us-east-1"}}},
		{"empty provider", "", map[string][]string{"test:thing": {"us-east-1"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("RegisterServices did not panic; the table would register and never be found")
				}
			}()
			regions.RegisterServices(c.provider, c.table)
		})
	}
}

func TestRegisterServicesRoundTrips(t *testing.T) {
	regions.RegisterServices("test", map[string][]string{"test:thing": {"r2", "r1"}})
	if known, avail := regions.ServiceAvailable("test:thing", "r1"); !known || !avail {
		t.Errorf("ServiceAvailable(test:thing, r1) = (%v, %v); want (true, true)", known, avail)
	}
	if known, avail := regions.ServiceAvailable("test:thing", "r3"); !known || avail {
		t.Errorf("ServiceAvailable(test:thing, r3) = (%v, %v); want (true, false)", known, avail)
	}
	if got := regions.ServiceRegions("test:thing"); !slices.Equal(got, []string{"r1", "r2"}) {
		t.Errorf("ServiceRegions(test:thing) = %v; want [r1 r2]", got)
	}
	// Re-registering replaces rather than merges, so a shrinking table cannot
	// leave a stale row answering for a service the provider dropped.
	regions.RegisterServices("test", map[string][]string{"test:other": {"r1"}})
	if known, _ := regions.ServiceAvailable("test:thing", "r1"); known {
		t.Error("re-registering left a stale row for test:thing")
	}
}
