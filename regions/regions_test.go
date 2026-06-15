package regions

import (
	"slices"
	"testing"
)

func TestFor(t *testing.T) {
	for _, p := range []string{"aws", "azure", "gcp"} {
		got := For(p)
		if len(got) == 0 {
			t.Errorf("For(%q) is empty", p)
		}
		if !slices.IsSorted(got) {
			t.Errorf("For(%q) is not sorted: %v", p, got)
		}
	}
	if For("unknown") != nil {
		t.Errorf("For(unknown) = non-nil; want nil")
	}
	// Returned slice is a copy: mutating it must not affect the source.
	a := For("aws")
	if len(a) > 0 {
		a[0] = "MUTATED"
		if slices.Contains(For("aws"), "MUTATED") {
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
		if got := Valid(c.provider, c.region); got != c.want {
			t.Errorf("Valid(%q, %q) = %v; want %v", c.provider, c.region, got, c.want)
		}
	}
}
