package aws

import (
	"errors"
	"testing"

	"github.com/aws/smithy-go"
)

func TestIsSCPExplicitDeny(t *testing.T) {
	scpDeny := &smithy.GenericAPIError{
		Code:    "AccessDeniedException",
		Message: "User: arn:aws:sts::980523845517:assumed-role/Admin/will is not authorized to perform: bedrock:ListGuardrails with an explicit deny in a service control policy: arn:aws:organizations::670108570542:policy/o-ci13gzxxrv/service_control_policy/p-2dkxgl4o",
	}
	plainDeny := &smithy.GenericAPIError{
		Code:    "AccessDeniedException",
		Message: "User: arn:... is not authorized to perform: bedrock:ListGuardrails",
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"scp explicit deny", scpDeny, true},
		{"plain iam deny", plainDeny, false},
		{"non-api err", errors.New("network error"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSCPExplicitDeny(tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIsMigrationRequiredIAMDeny(t *testing.T) {
	migrate := &smithy.GenericAPIError{
		Code:    "AccessDeniedException",
		Message: "To use this feature, please obtain the required permissions. Migrate the policies in your account to use the new IAM actions.",
	}
	plainDeny := &smithy.GenericAPIError{
		Code:    "AccessDeniedException",
		Message: "User: arn:... is not authorized to perform: bcmpricingcalculator:ListBillScenarios",
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"migration message", migrate, true},
		{"plain iam deny", plainDeny, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMigrationRequiredIAMDeny(tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestIsFMSAdminOnlyDenial(t *testing.T) {
	adminOnly := &smithy.GenericAPIError{
		Code:    "AccessDeniedException",
		Message: "Operation ListPolicies is only available to AWS Firewall Manager Administrators",
	}
	notEnabled := &smithy.GenericAPIError{
		Code:    "AccessDeniedException",
		Message: "No default admin could be found for account",
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"admin only", adminOnly, true},
		{"not enabled (separate predicate)", notEnabled, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFMSAdminOnlyDenial(tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// TestIsStorageLensHomeRegionGap separates the two things S3 Storage Lens
// returns as AccessDenied. Both message variants are captured off the wire in
// ap-northeast-3, where the account genuinely holds the permission.
//
// The row that carries the risk is "real iam deny": this predicate silences a
// warning, so a false positive hides a permission gap as an empty region and
// nothing downstream would notice — the pair reports zero resources either way.
func TestIsStorageLensHomeRegionGap(t *testing.T) {
	configurations := &smithy.GenericAPIError{
		Code:    "AccessDenied",
		Message: "Region is not supported as home region for S3 Storage Lens",
	}
	groups := &smithy.GenericAPIError{
		Code:    "AccessDenied",
		Message: "This Region isn't a supported home Region for S3 Storage Lens groups.",
	}
	realDeny := &smithy.GenericAPIError{
		Code:    "AccessDenied",
		Message: "User: arn:aws:sts::228886154857:assumed-role/DiscoScanner/x is not authorized to perform: s3:ListStorageLensConfigurations",
	}
	// What the SDK produces when the envelope was NOT repaired. It must not
	// match: silencing it would hide every unparseable 403, not just this one.
	unrepaired := &smithy.GenericAPIError{Code: "UnknownError", Message: "UnknownError"}
	// Same words, wrong code. Only the access-denied family carries the
	// availability meaning; a validation failure that happens to mention a home
	// region is a caller error and must still surface.
	wrongCode := &smithy.GenericAPIError{
		Code:    "ValidationException",
		Message: "home region must be supplied",
	}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"configurations variant", configurations, true},
		{"groups variant", groups, true},
		{"real iam deny", realDeny, false},
		{"unrepaired unknown error", unrepaired, false},
		{"home-region text under a non-denial code", wrongCode, false},
		{"non-api err", errors.New("dial tcp: connection refused"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStorageLensHomeRegionGap(tc.err); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
