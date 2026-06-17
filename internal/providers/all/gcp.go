//go:build !slim || gcp

package all

import (
	// Blank-importing the gcp package runs its init() — registering the Scanner
	// and coverage Provider. Gated so `-tags slim` without `gcp` excludes it and
	// the gcp SDK is never linked.
	_ "codeberg.org/icearp/disco/internal/providers/gcp"
)
