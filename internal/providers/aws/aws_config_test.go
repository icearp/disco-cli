package aws

import (
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
)

// TestChainAssumeRoles_Empty returns nil for empty input — caller falls back
// to baseCfg credentials.
func TestChainAssumeRoles_Empty(t *testing.T) {
	got := chainAssumeRoles(sdkaws.Config{Region: "us-east-1"}, nil)
	if got != nil {
		t.Errorf("expected nil for empty chain, got %#v", got)
	}
}

// TestChainAssumeRoles_BuildsCache verifies a non-empty chain returns a
// non-nil CredentialsCache. Constructing the cache exercises every hop
// (sts.NewFromConfig + stscreds.NewAssumeRoleProvider); actual STS calls
// only fire on first credentials retrieval, which we don't do here.
func TestChainAssumeRoles_BuildsCache(t *testing.T) {
	cache := chainAssumeRoles(sdkaws.Config{Region: "us-east-1"}, []string{
		"arn:aws:iam::111111111111:role/Hub",
		"arn:aws:iam::222222222222:role/Audit",
	})
	if cache == nil {
		t.Fatal("expected non-nil cache for two-hop chain")
	}
}
