package aws

import "testing"

// Greengrass V1 keeps the commercial hand list for aws accounts, which leaves
// out regions the SDK lists, and reads the SDK table for every other partition.
func TestGreengrassV1RegionSupported_PerPartition(t *testing.T) {
	cases := []struct {
		partition, region string
		want              bool
	}{
		{"aws", "us-east-1", true},
		{"aws", "ca-central-1", false}, // SDK lists it; the commercial list does not
		{"aws-cn", "cn-north-1", true},
		{"aws-cn", "cn-northwest-1", false},
		{"aws-us-gov", "us-gov-west-1", true},
	}
	for _, c := range cases {
		if got := greengrassV1RegionSupported(testAccountIn(c.partition), c.region); got != c.want {
			t.Errorf("greengrassV1RegionSupported(%s, %s) = %v; want %v", c.partition, c.region, got, c.want)
		}
	}
}
