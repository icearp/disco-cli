//go:build !slim || aws

package all

import (
	// Blank-importing the awsregions leaf runs its init(), registering the AWS
	// region list into the regions registry. Gated so `-tags slim` without `aws`
	// excludes it.
	_ "codeberg.org/icearp/disco/internal/providers/aws/awsregions"
)
