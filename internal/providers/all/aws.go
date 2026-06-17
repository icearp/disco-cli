//go:build !slim || aws

package all

import (
	// Blank-importing the aws package runs its init() — registering the Scanner
	// and coverage Provider. Gated so `-tags slim` without `aws` excludes it and
	// the aws SDK is never linked.
	_ "codeberg.org/icearp/disco/internal/providers/aws"
)
