package azure

import (
	"slices"

	"codeberg.org/icearp/disco/internal/providers/azure/azureregions"
)

// RegionNames implements providers.RegionNamer. Returns a clone so callers
// can't mutate the package-level list. The location list itself is the SDK-free
// source of truth in the azureregions leaf package.
func (s *Scanner) RegionNames() []string { return slices.Clone(azureregions.Regions) }
