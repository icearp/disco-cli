package awsregions

import (
	"regexp"
	"slices"
	"testing"
)

// TestGeneratedPartitionsAnchors pins facts about the SDK's partition table
// that a regeneration must not silently lose: each partition disco is asked to
// support is present with the DNS suffix AWS serves it under, and every row is
// usable by the region lookup built on it.
func TestGeneratedPartitionsAnchors(t *testing.T) {
	byID := make(map[string]Partition, len(Partitions))
	for _, p := range Partitions {
		if _, dup := byID[p.ID]; dup {
			t.Errorf("partition %s listed twice; the first match would shadow the second", p.ID)
		}
		byID[p.ID] = p
		re, err := regexp.Compile(p.RegionRegex)
		if err != nil {
			t.Errorf("partition %s regionRegex %q does not compile: %v", p.ID, p.RegionRegex, err)
			continue
		}
		if p.ImplicitGlobalRegion != "" && !re.MatchString(p.ImplicitGlobalRegion) {
			t.Errorf("partition %s implicit global region %q fails its own regionRegex", p.ID, p.ImplicitGlobalRegion)
		}
		if !slices.IsSorted(p.Regions) {
			t.Errorf("partition %s regions are unsorted, so regenerating produces a spurious diff", p.ID)
		}
	}

	for id, suffix := range map[string]string{
		"aws":        "amazonaws.com",
		"aws-cn":     "amazonaws.com.cn",
		"aws-us-gov": "amazonaws.com",
		"aws-eusc":   "amazonaws.eu",
		"aws-iso":    "c2s.ic.gov",
	} {
		p, ok := byID[id]
		if !ok {
			t.Errorf("partition %s is missing from the table", id)
			continue
		}
		if p.DNSSuffix != suffix {
			t.Errorf("partition %s DNSSuffix = %q; want %q", id, p.DNSSuffix, suffix)
		}
	}
	if !slices.Contains(byID["aws-cn"].Regions, "cn-north-1") {
		t.Errorf("aws-cn regions %v omit cn-north-1", byID["aws-cn"].Regions)
	}
}
