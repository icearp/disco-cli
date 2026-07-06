//go:build !slim || azure

package all

import (
	// Blank-importing azure runs its init(), registering the Scanner and
	// coverage Provider. Gated so `-tags slim` without `azure` excludes it —
	// the azure SDK is never linked.
	_ "codeberg.org/icearp/disco/internal/providers/azure"
)
