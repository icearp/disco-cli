//go:build !slim || aws

package all

// Blank-importing the awsregions leaf runs its init(), registering the AWS
// region list into the regions registry. Gated so `-tags slim` without `aws`
// excludes it.
import _ "codeberg.org/icearp/disco/internal/providers/aws/awsregions"
