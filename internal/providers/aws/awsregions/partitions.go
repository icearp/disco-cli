package awsregions

import (
	"regexp"
	"slices"
)

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

// PartitionForRegion returns the partition a region belongs to, and false
// when no partition lists it or matches it.
//
// The order mirrors the SDK's own lookup (awsrulesfn.GetPartition): a region a
// partition lists by name wins over every regex, and only then are the regexes
// tried in table order. Unlike the SDK, an unmatched region is not assigned to
// the commercial partition.
func PartitionForRegion(region string) (string, bool) {
	for _, p := range Partitions {
		if slices.Contains(p.Regions, region) {
			return p.ID, true
		}
	}
	for i, re := range partitionRegexps {
		if re.MatchString(region) {
			return Partitions[i].ID, true
		}
	}
	return "", false
}

// partitionRegexps holds each partition's compiled regionRegex, index-aligned
// with Partitions. The generator rejects a pattern that does not
// compile, so MustCompile cannot fire on a committed table.
var partitionRegexps = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(Partitions))
	for i, p := range Partitions {
		out[i] = regexp.MustCompile(p.RegionRegex)
	}
	return out
}()
