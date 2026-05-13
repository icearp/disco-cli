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

// TestEmulatorAccountIDOverride pins F28's prod-safety gate: the
// override only fires when AWS_ENDPOINT_URL is also set. Prod scanners
// never set AWS_ENDPOINT_URL (the SDK reaches real AWS), so the env is
// inert outside emulator mode.
func TestEmulatorAccountIDOverride(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		acctID   string
		want     string
	}{
		{"both unset", "", "", ""},
		// PROD-SAFETY: AWS_ENDPOINT_URL unset means the override is
		// ignored even when DISCO_CLOUD_ACCOUNT_ID names a real-looking
		// account. This is the load-bearing assertion.
		{"acctID set without endpoint", "", "999999999999", ""},
		{"endpoint set without acctID", "http://localhost:4566", "", ""},
		{"both set", "http://localhost:4566", "999999999999", "999999999999"},
		{"endpoint whitespace only", "   ", "999999999999", ""},
		{"acctID whitespace only", "http://localhost:4566", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("AWS_ENDPOINT_URL", c.endpoint)
			t.Setenv("DISCO_CLOUD_ACCOUNT_ID", c.acctID)
			if got := emulatorAccountIDOverride(); got != c.want {
				t.Errorf("emulatorAccountIDOverride() = %q; want %q", got, c.want)
			}
		})
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
