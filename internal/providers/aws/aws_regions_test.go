package aws

import (
	"slices"
	"testing"

	"codeberg.org/icearp/disco/internal/providers/aws/awsregions"
)

func TestExpandAllRegions(t *testing.T) {
	full := awsregions.Regions
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"all sentinel", []string{"all"}, full},
		{"uppercase", []string{"ALL"}, full},
		{"padded", []string{" all "}, full},
		{"sentinel wins over specifics", []string{"all", "us-east-1"}, full},
		{"specific list unchanged", []string{"us-east-1", "us-west-2"}, []string{"us-east-1", "us-west-2"}},
		{"nil unchanged", nil, nil},
		{"empty unchanged", []string{}, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandAllRegions(tc.in)
			if !slices.Equal(got, tc.want) {
				t.Errorf("expandAllRegions(%v) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestExpandAllRegionsCloneSemantics guards that the "all" expansion hands back
// a fresh slice — mutating it must not corrupt the package-level region list
// (mirrors TestRegionNamesCloneSemantics).
func TestExpandAllRegionsCloneSemantics(t *testing.T) {
	before := slices.Clone(awsregions.Regions)
	got := expandAllRegions([]string{"all"})
	if len(got) == 0 {
		t.Fatal("expandAllRegions([all]) returned empty")
	}
	got[0] = "mutated-region"
	if !slices.Equal(awsregions.Regions, before) {
		t.Fatalf("mutating expandAllRegions result altered awsregions.Regions: %v", awsregions.Regions)
	}
}
