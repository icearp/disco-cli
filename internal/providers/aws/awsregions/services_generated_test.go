package awsregions

import (
	"slices"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/regions"
)

// TestGeneratedServiceRegionsAnchors pins the properties the scanner's skip
// decision depends on. It is not a snapshot of the whole table — the table
// changes whenever the SDK is bumped, and that is expected — but each anchor
// below is a fact about AWS that a regeneration must not silently invert.
func TestGeneratedServiceRegionsAnchors(t *testing.T) {
	if len(ServiceRegions) == 0 {
		t.Fatal("ServiceRegions is empty; region scoping is off for every service (regenerate with `make gen-regions`)")
	}

	// Each straggler that motivated the table, with the region it stalled a
	// scan in for minutes and a region it genuinely serves. Asserting only the
	// absence would pass against a table that lost the service entirely — an
	// absent service means "no opinion", which is scanned, not skipped.
	for _, c := range []struct {
		service        string
		absent, listed string
	}{
		{"aws:cassandra", "ap-northeast-3", "ap-south-1"},
		{"aws:connect-campaigns-v2", "ap-northeast-3", "us-east-1"},
		{"aws:connect-campaigns-v2", "ap-south-1", "us-east-1"},
		{"aws:connect-campaigns", "ap-south-1", "us-east-1"},
		{"aws:ec2", "", "us-east-1"},
	} {
		got, ok := ServiceRegions[c.service]
		if !ok {
			t.Errorf("%s is absent from the table, so it has no opinion and is scanned everywhere", c.service)
			continue
		}
		if c.absent != "" && slices.Contains(got, c.absent) {
			t.Errorf("%s lists %s; AWS does not offer it there and scanning it stalls until the per-service budget expires", c.service, c.absent)
		}
		if !slices.Contains(got, c.listed) {
			t.Errorf("%s omits %s, where it is genuinely offered; scoping would skip real resources", c.service, c.listed)
		}
	}

	supported := make(map[string]bool, len(Regions))
	for _, r := range Regions {
		supported[r] = true
	}
	for service, list := range ServiceRegions {
		if !strings.HasPrefix(service, "aws:") {
			t.Errorf("service key %q lacks the aws: prefix; regions.ServiceAvailable derives the provider from it and would never find this row", service)
		}
		if len(list) == 0 {
			t.Errorf("%s has an empty region list; an empty list reads as no opinion, so the row is dead weight", service)
		}
		if !slices.IsSorted(list) {
			t.Errorf("%s region list is not sorted, so regenerating produces a spurious diff", service)
		}
		for _, r := range list {
			if !supported[r] {
				t.Errorf("%s lists %q, which is not a region disco scans (FIPS pseudo-region, non-region endpoint key, or another partition)", service, r)
			}
		}
	}
}

// TestGeneratedServiceRegionsAreRegistered proves the init() in the generated
// file actually reaches the public registry. Without it the table would be
// correct and inert: every lookup would report no opinion and the scanner would
// go on probing dead regions, with nothing failing.
func TestGeneratedServiceRegionsAreRegistered(t *testing.T) {
	known, avail := regions.ServiceAvailable("aws:cassandra", "us-east-1")
	if !known || !avail {
		t.Fatalf("regions.ServiceAvailable(aws:cassandra, us-east-1) = (%v, %v), want (true, true)", known, avail)
	}
	if known, avail := regions.ServiceAvailable("aws:cassandra", "ap-northeast-3"); !known || avail {
		t.Errorf("regions.ServiceAvailable(aws:cassandra, ap-northeast-3) = (%v, %v), want (true, false)", known, avail)
	}
}
