package aws

import (
	"strings"
	"testing"
)

// TestAWSResourceMirrorsUpstream is the regression ratchet for the disco-type
// naming rule: the resource segment mirrors the upstream resource name (the
// CloudFormation ∪ Service Reference union the alias map targets). Acronym
// casing (VPC vs Vpc) and the service segment are bridged by the alias map, so
// the comparison is on the hyphen-stripped, lower-cased forms. A new AWS type
// that de-stutters, renames, or abbreviates its resource relative to upstream
// (e.g. backup:vault for BackupVault, docdb:cluster for DBCluster) fails here.
func TestAWSResourceMirrorsUpstream(t *testing.T) {
	for disco, upstream := range (coverageProvider{}).Aliases() {
		dp := strings.SplitN(disco, ":", 3)
		up := strings.SplitN(upstream, "::", 3)
		if len(dp) != 3 || len(up) != 3 {
			continue
		}
		if strings.ReplaceAll(dp[2], "-", "") != strings.ToLower(up[2]) {
			t.Errorf("disco %q resource %q does not mirror upstream %q resource %q",
				disco, dp[2], upstream, up[2])
		}
	}
}
