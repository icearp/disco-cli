//go:build !slim || azure

package all

import (
	// Blank-importing azureregions runs its init(), registering Azure's
	// location list into the regions registry. Gated so `-tags slim` without
	// `azure` excludes it.
	_ "codeberg.org/icearp/disco/internal/providers/azure/azureregions"
)
