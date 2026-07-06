package aws

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestExplainConfigLoadError verifies the assume-role failure is translated into
// an actionable hint while preserving the wrapped SDK error, and that non-assume
// errors pass through unchanged.
func TestExplainConfigLoadError(t *testing.T) {
	const roleARN = "arn:aws:iam::131546573061:role/OrganizationAccountAccessRole"
	arErr := awsconfig.SharedConfigAssumeRoleError{Profile: "default", RoleARN: roleARN, Err: nil}

	t.Run("assume-role hint uses invoked profile", func(t *testing.T) {
		got := explainConfigLoadError(arErr, "sandbox")
		msg := got.Error()
		for _, want := range []string{
			"aws configure export-credentials",
			"--profile sandbox",
			roleARN,
			`source profile "default"`,
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("explainConfigLoadError msg missing %q\ngot: %s", want, msg)
			}
		}
		// Wrap preserved: callers/logging can still recover the SDK error.
		var rt awsconfig.SharedConfigAssumeRoleError
		if !errors.As(got, &rt) {
			t.Errorf("explainConfigLoadError dropped the wrapped SharedConfigAssumeRoleError")
		}
	})

	t.Run("empty invoked profile falls back to source profile", func(t *testing.T) {
		msg := explainConfigLoadError(arErr, "").Error()
		if !strings.Contains(msg, "--profile default") {
			t.Errorf("want fallback to --profile default; got: %s", msg)
		}
	})

	t.Run("non-assume error passes through", func(t *testing.T) {
		base := errors.New("boom")
		got := explainConfigLoadError(base, "sandbox")
		if got.Error() != "load aws sdk config: boom" {
			t.Errorf("passthrough = %q; want %q", got.Error(), "load aws sdk config: boom")
		}
		if strings.Contains(got.Error(), "export-credentials") {
			t.Errorf("non-assume error should carry no export hint; got: %s", got.Error())
		}
		if !errors.Is(got, base) {
			t.Errorf("passthrough dropped the wrapped error")
		}
	})
}

// TestChainAssumeRoles_Empty returns nil for empty input — caller falls back
// to baseCfg credentials.
func TestChainAssumeRoles_Empty(t *testing.T) {
	got := chainAssumeRoles(sdkaws.Config{Region: "us-east-1"}, nil, "")
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
	}, "disco-scan")
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

// TestSetIncludeServiceQuotas verifies the capability-interface setter flips the
// Scanner field that opts the default-off aws:servicequotas scanner into the scan.
func TestSetIncludeServiceQuotas(t *testing.T) {
	s := &Scanner{}
	if s.includeServiceQuotas {
		t.Fatal("default should be false (servicequotas opt-in)")
	}
	s.SetIncludeServiceQuotas(true)
	if !s.includeServiceQuotas {
		t.Error("SetIncludeServiceQuotas(true) did not set the field")
	}
	s.SetIncludeServiceQuotas(false)
	if s.includeServiceQuotas {
		t.Error("SetIncludeServiceQuotas(false) did not clear the field")
	}
}

// TestAssumeRoleOpts verifies the shared AssumeRole option closure sets only
// the fields it was given, and returns nil when there's nothing to set (so
// callers pass a no-op extra rather than an empty closure).
func TestAssumeRoleOpts(t *testing.T) {
	if assumeRoleOpts("", "") != nil {
		t.Error("both empty should return nil closure")
	}

	cases := []struct {
		name             string
		externalID       string
		sourceIdentity   string
		wantExternalID   string
		wantSourceIdenty string
	}{
		{"external only", "ext", "", "ext", ""},
		{"source only", "", "ident", "", "ident"},
		{"both", "ext", "ident", "ext", "ident"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := &stscreds.AssumeRoleOptions{}
			fn := assumeRoleOpts(c.externalID, c.sourceIdentity)
			if fn == nil {
				t.Fatal("expected non-nil closure")
			}
			fn(o)
			gotExt := ""
			if o.ExternalID != nil {
				gotExt = *o.ExternalID
			}
			gotSrc := ""
			if o.SourceIdentity != nil {
				gotSrc = *o.SourceIdentity
			}
			if gotExt != c.wantExternalID {
				t.Errorf("ExternalID = %q, want %q", gotExt, c.wantExternalID)
			}
			if gotSrc != c.wantSourceIdenty {
				t.Errorf("SourceIdentity = %q, want %q", gotSrc, c.wantSourceIdenty)
			}
		})
	}
}

// TestResolveSourceIdentity covers the off / auto / literal resolution.
func TestResolveSourceIdentity(t *testing.T) {
	const scanID = "0123456789abcdef0123456789abcdef"
	cases := []struct {
		configured string
		want       string
	}{
		{"", ""},
		{"auto", scanID},
		{"my-operator", "my-operator"},
	}
	for _, c := range cases {
		if got := resolveSourceIdentity(c.configured, scanID); got != c.want {
			t.Errorf("resolveSourceIdentity(%q) = %q, want %q", c.configured, got, c.want)
		}
	}
}

// TestValidateSourceIdentity pins the AWS SourceIdentity constraint (2-64 chars
// from [A-Za-z0-9_+=,.@-]). The 32-hex scan ID must always pass.
func TestValidateSourceIdentity(t *testing.T) {
	valid := []string{
		"0123456789abcdef0123456789abcdef", // scan-ID shape
		"a@b.c-1",
		"ab",
		"tenant_42+role=,x.y@z-w",
	}
	for _, v := range valid {
		if err := validateSourceIdentity(v); err != nil {
			t.Errorf("validateSourceIdentity(%q) = %v, want nil", v, err)
		}
	}
	invalid := []string{
		"",                      // empty
		"a",                     // 1 char
		"has space",             // disallowed char
		"bad/slash",             // disallowed char
		strings.Repeat("a", 65), // >64
	}
	for _, v := range invalid {
		if err := validateSourceIdentity(v); err == nil {
			t.Errorf("validateSourceIdentity(%q) = nil, want error", v)
		}
	}
}

// TestSetSourceIdentity_PinsScannerState verifies the capability setter stores
// the raw configured value unchanged.
func TestSetSourceIdentity_PinsScannerState(t *testing.T) {
	s := &Scanner{}
	s.SetSourceIdentity("auto")
	if s.sourceIdentity != "auto" {
		t.Errorf("sourceIdentity = %q, want auto", s.sourceIdentity)
	}
}

// TestFilterToEnabled partitions a requested region list against the enabled
// set, preserving order; disabled regions land in skipped (not silently
// dropped) so the caller can warn about them.
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
