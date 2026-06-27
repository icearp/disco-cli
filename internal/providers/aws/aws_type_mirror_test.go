package aws

import (
	"strings"
	"testing"
)

// TestAWSResourceMirrorsCFN is the regression ratchet for the disco-type naming
// rule: the resource segment mirrors the upstream CloudFormation resource name.
// Acronym casing (VPC vs Vpc) and the service segment are bridged by the alias
// map, so the comparison is on the hyphen-stripped, lower-cased forms. A new AWS
// type that de-stutters, renames, or abbreviates its resource relative to CFN
// (e.g. backup:vault for BackupVault, docdb:cluster for DBCluster) fails here.
func TestAWSResourceMirrorsCFN(t *testing.T) {
	for disco, cfn := range (coverageProvider{}).Aliases() {
		dp := strings.SplitN(disco, ":", 3)
		cp := strings.SplitN(cfn, "::", 3)
		if len(dp) != 3 || len(cp) != 3 {
			continue
		}
		if strings.ReplaceAll(dp[2], "-", "") != strings.ToLower(cp[2]) {
			t.Errorf("disco %q resource %q does not mirror CFN %q resource %q",
				disco, dp[2], cfn, cp[2])
		}
	}
}
