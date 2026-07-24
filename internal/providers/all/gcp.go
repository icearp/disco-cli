//go:build !slim || gcp

package all

import (
	// Blank-importing gcp runs its init(), registering the Scanner and coverage
	// Provider. Gated so `-tags slim` without `gcp` excludes it — the gcp SDK
	// is never linked.
	_ "github.com/icearp/disco-cli/internal/providers/gcp"
)
