package aws

import (
	"context"
	"errors"
	"slices"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

// TestFilterToEnabled partitions a requested region list against the enabled
// set, preserving order, and asserts disabled regions land in skipped (not
// silently dropped) so the caller can warn about them.
func TestFilterToEnabled(t *testing.T) {
	cases := []struct {
		name        string
		requested   []string
		enabled     map[string]bool
		wantKept    []string
		wantSkipped []string
	}{
		{
			name:      "all enabled",
			requested: []string{"us-east-1", "us-west-2"},
			enabled:   map[string]bool{"us-east-1": true, "us-west-2": true},
			wantKept:  []string{"us-east-1", "us-west-2"},
		},
		{
			name:        "some disabled, order preserved",
			requested:   []string{"us-east-1", "af-south-1", "eu-west-1", "ap-east-1"},
			enabled:     map[string]bool{"us-east-1": true, "eu-west-1": true},
			wantKept:    []string{"us-east-1", "eu-west-1"},
			wantSkipped: []string{"af-south-1", "ap-east-1"},
		},
		{
			name:        "all disabled",
			requested:   []string{"af-south-1", "ap-east-1"},
			enabled:     map[string]bool{"us-east-1": true},
			wantSkipped: []string{"af-south-1", "ap-east-1"},
		},
		{
			name:        "empty enabled set skips everything",
			requested:   []string{"us-east-1", "eu-west-1"},
			enabled:     map[string]bool{},
			wantSkipped: []string{"us-east-1", "eu-west-1"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kept, skipped := filterToEnabled(c.requested, c.enabled)
			if !slices.Equal(kept, c.wantKept) {
				t.Errorf("filterToEnabled(%v) kept = %v; want %v", c.requested, kept, c.wantKept)
			}
			if !slices.Equal(skipped, c.wantSkipped) {
				t.Errorf("filterToEnabled(%v) skipped = %v; want %v", c.requested, skipped, c.wantSkipped)
			}
		})
	}
}

// stubDescribeRegions is a describeRegionsAPI seam for enabledRegionSet tests.
type stubDescribeRegions struct {
	out *ec2.DescribeRegionsOutput
	err error
}

func (s stubDescribeRegions) DescribeRegions(context.Context, *ec2.DescribeRegionsInput, ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	return s.out, s.err
}

// TestEnabledRegionSet builds the enabled-region set from a DescribeRegions
// response and tolerates a nil RegionName entry (no panic, just skipped).
func TestEnabledRegionSet(t *testing.T) {
	out := &ec2.DescribeRegionsOutput{Regions: []ec2types.Region{
		{RegionName: sp("us-east-1")},
		{RegionName: sp("eu-west-1")},
		{RegionName: nil},
	}}
	got, err := enabledRegionSet(context.Background(), stubDescribeRegions{out: out})
	if err != nil {
		t.Fatalf("enabledRegionSet returned error: %v", err)
	}
	want := map[string]bool{"us-east-1": true, "eu-west-1": true}
	if len(got) != len(want) {
		t.Fatalf("enabledRegionSet = %v; want %v", got, want)
	}
	for r := range want {
		if !got[r] {
			t.Errorf("enabledRegionSet missing %q; got %v", r, got)
		}
	}
}

// TestEnabledRegionSet_Error propagates the probe error so the caller can fall
// back to the full region list rather than scanning nothing.
func TestEnabledRegionSet_Error(t *testing.T) {
	sentinel := errors.New("ec2:DescribeRegions denied")
	got, err := enabledRegionSet(context.Background(), stubDescribeRegions{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Errorf("enabledRegionSet err = %v; want %v", err, sentinel)
	}
	if got != nil {
		t.Errorf("enabledRegionSet on error = %v; want nil", got)
	}
}
