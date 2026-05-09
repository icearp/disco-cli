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

// TestSetRoleOverride_PinsScannerState verifies the capability interface
// stores both fields and is read back unchanged by SetRoleOverride.
func TestSetRoleOverride_PinsScannerState(t *testing.T) {
	s := &Scanner{}
	s.SetRoleOverride("arn:aws:iam::111111111111:role/Disco", "ext-id-123")
	if s.roleARN != "arn:aws:iam::111111111111:role/Disco" {
		t.Errorf("roleARN = %q", s.roleARN)
	}
	if s.externalID != "ext-id-123" {
		t.Errorf("externalID = %q", s.externalID)
	}
	// Empty roleARN clears.
	s.SetRoleOverride("", "")
	if s.roleARN != "" || s.externalID != "" {
		t.Errorf("SetRoleOverride should have cleared, got %q / %q", s.roleARN, s.externalID)
	}
}
