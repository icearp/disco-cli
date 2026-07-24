package azure

import (
	"slices"

	"github.com/icearp/disco-cli/internal/providers/azure/azureregions"
)

// RegionNames implements providers.RegionNamer. Returns a clone so callers
// can't mutate the package-level list; azureregions leaf package is the
// SDK-free source of truth.
func (s *Scanner) RegionNames() []string { return slices.Clone(azureregions.Regions) }
