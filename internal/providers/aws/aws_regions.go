package aws

import (
	"slices"
	"strings"

	"codeberg.org/icearp/disco/internal/providers/aws/awsregions"
)

// RegionNames implements providers.RegionNamer. Returns a clone so callers
// can't mutate the package-level list. The region list itself is the SDK-free
// source of truth in the awsregions leaf package.
func (s *Scanner) RegionNames() []string { return slices.Clone(awsregions.Regions) }

// expandAllRegions replaces the "all" sentinel (any case) with the full static
// region list; enabledScanRegions later trims it to the account's opted-in set.
// Returns a clone so callers can't mutate the package list. Non-sentinel input
// is returned unchanged.
func expandAllRegions(regions []string) []string {
	for _, r := range regions {
		if strings.EqualFold(strings.TrimSpace(r), "all") {
			return slices.Clone(awsregions.Regions)
		}
	}
	return regions
}
