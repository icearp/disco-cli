//go:build !slim || gcp

package all

import (
	// Blank-importing gcpregions runs its init(), registering GCP's region
	// list into the regions registry. Gated so `-tags slim` without `gcp`
	// excludes it.
	_ "github.com/icearp/disco-cli/internal/providers/gcp/gcpregions"
)
