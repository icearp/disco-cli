//go:build !slim || azure

package all

import (
	// Blank-importing azureregions runs its init(), registering Azure's
	// location list into the regions registry. Gated so `-tags slim` without
	// `azure` excludes it.
	_ "github.com/icearp/disco-cli/internal/providers/azure/azureregions"
)
