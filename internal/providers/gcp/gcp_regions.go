package gcp

import (
	"slices"

	"codeberg.org/icearp/disco/internal/providers/gcp/gcpregions"
)

// RegionNames implements providers.RegionNamer. Returns a clone so callers
// can't mutate the package-level list. The region list itself is the SDK-free
// source of truth in the gcpregions leaf package.
func (s *Scanner) RegionNames() []string { return slices.Clone(gcpregions.Regions) }
