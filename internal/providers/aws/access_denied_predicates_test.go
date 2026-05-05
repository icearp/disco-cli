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
