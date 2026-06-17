//go:build !slim || gcp

package all

import (
	// Blank-importing the gcpregions leaf runs its init(), registering the GCP
	// region list into the regions registry. Gated so `-tags slim` without `gcp`
	// excludes it.
	_ "codeberg.org/icearp/disco/internal/providers/gcp/gcpregions"
)
