// Package awsregions is disco's static list of supported AWS
// commercial-partition regions. Deliberately SDK-free (stdlib only) so external
// callers — the public codeberg.org/icearp/disco/regions package and, through
// it, the SaaS control plane — can import the list without linking the AWS SDK.
//
// Excludes GovCloud (us-gov-east-1, us-gov-west-1) and China (cn-north-1,
// cn-northwest-1) partitions — those need separate creds and are not in the
// default AWS credential chain.
//
// Refresh when AWS GAs a new region. Source: aws-cli's
// `aws ec2 describe-regions --all-regions` against a recent SDK.
package awsregions

import "codeberg.org/icearp/disco/regions"

func init() { regions.Register("aws", Regions) }

// Regions is the supported AWS region list. Treat as read-only; callers that
// expose it (e.g. RegionNames) clone first.
var Regions = []string{
	"af-south-1",
	"ap-east-1",
	"ap-east-2",
	"ap-northeast-1",
	"ap-northeast-2",
	"ap-northeast-3",
	"ap-south-1",
	"ap-south-2",
	"ap-southeast-1",
	"ap-southeast-2",
	"ap-southeast-3",
	"ap-southeast-4",
	"ap-southeast-5",
	"ap-southeast-6",
	"ap-southeast-7",
	"ca-central-1",
	"ca-west-1",
	"eu-central-1",
	"eu-central-2",
	"eu-north-1",
	"eu-south-1",
	"eu-south-2",
	"eu-west-1",
	"eu-west-2",
	"eu-west-3",
	"il-central-1",
	"me-central-1",
	"me-south-1",
	"mx-central-1",
	"sa-east-1",
	"us-east-1",
	"us-east-2",
	"us-west-1",
	"us-west-2",
}
