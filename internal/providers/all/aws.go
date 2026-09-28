//go:build !slim || aws

package all

import (
	// Blank-importing aws runs its init(), registering the Scanner and coverage
	// Provider. Gated so `-tags slim` without `aws` excludes it — the aws SDK
	// is never linked.
	_ "github.com/icearp/disco-cli/internal/providers/aws"
	// Registers the aws SDK inventory extractor and pairing resolver.
	_ "github.com/icearp/disco-cli/internal/providers/aws/awsinventory"
)
