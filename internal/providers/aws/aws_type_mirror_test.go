package aws

import (
	"strings"
	"testing"
)

// TestAWSResourceMirrorsUpstream is the regression ratchet for the disco-type
// naming rule: the resource segment mirrors the upstream resource name (the
// CloudFormation ∪ Service Reference union the alias map targets). The alias
// map bridges acronym casing (VPC vs Vpc) and the service segment, so this
// compares hyphen-stripped, lower-cased forms. A new AWS type that de-stutters,
// renames, or abbreviates its resource relative to upstream (e.g. backup:vault
// for BackupVault, docdb:cluster for DBCluster) fails here.
//
// Hyphens (and spaces) are stripped on both sides: CloudFormation resource
// names are PascalCase with no separators (InvestigationGroup), while Service
// Reference spells the same resources lower-cased and hyphenated
// (investigation-group, private-connection) or space-separated (gameliftstreams
// "stream group"). All are valid alias targets, so separator placement is
// ignored — cosmetic, not a semantic rename — while abbreviation and
// de-stuttering still get caught.
func TestAWSResourceMirrorsUpstream(t *testing.T) {
	// strip removes cosmetic separators and, mirroring canonResource in
	// aws_coverage.go, a trailing "resource" — Service Reference suffixes every
	// resource in some services (mgn's SourceServerResource) where disco drops
	// the suffix per the type-naming rule. Guards against reducing a segment to
	// empty (AWS::ApiGateway::Resource stays "resource").
	strip := func(s string) string {
		s = strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(s), "-", ""), " ", "")
		if stem := strings.TrimSuffix(s, "resource"); stem != "" && stem != s {
			s = stem
		}
		return s
	}
	for disco, upstream := range (coverageProvider{}).Aliases() {
		dp := strings.SplitN(disco, ":", 3)
		up := strings.SplitN(upstream, "::", 3)
		if len(dp) != 3 || len(up) != 3 {
			continue
		}
		if strip(dp[2]) != strip(strings.ToLower(up[2])) {
			t.Errorf("disco %q resource %q does not mirror upstream %q resource %q",
				disco, dp[2], upstream, up[2])
		}
	}
}
