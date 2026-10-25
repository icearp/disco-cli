package awsregions

// Partition is one AWS partition as the pinned aws-sdk-go-v2 describes it in
// internal/endpoints/awsrulesfn/partitions.json. The values are copied from
// that file by tools/genregions, never written by hand, so a partition the SDK
// does not know is one disco does not know either.
type Partition struct {
	ID          string
	RegionRegex string
	DNSSuffix   string
	// DualStackDNSSuffix is "" when partitions.json gives the partition none.
	DualStackDNSSuffix   string
	ImplicitGlobalRegion string
	// Regions are the partition's region keys exactly as partitions.json lists
	// them, which includes global pseudo-regions such as "aws-global". They
	// are not a scan list.
	Regions []string
}
