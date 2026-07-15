package gcp

import (
	"errors"
	"testing"

	"google.golang.org/api/googleapi"
)

func TestIsBillingDisabled(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "400 failedPrecondition billing",
			err:  &googleapi.Error{Code: 400, Message: "Billing is disabled for project 317820104764. Enable it by visiting https://console.cloud.google.com/billing/projects and associating your project with a billing account."},
			want: true,
		},
		{
			name: "403 has billing disabled",
			err:  &googleapi.Error{Code: 403, Message: "Project project-c812e421 has billing disabled. Please enable it."},
			want: true,
		},
		{
			name: "plain 403 IAM denial",
			err:  &googleapi.Error{Code: 403, Message: "You don't have permission to list roles in organizations/347918993576."},
			want: false,
		},
		{
			name: "api not enabled",
			err:  &googleapi.Error{Code: 403, Message: "Cloud Spanner API has not been used in project 317820104764 before or it is disabled."},
			want: false,
		},
		{
			name: "non-googleapi error",
			err:  errServiceDisabled,
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBillingDisabled(tc.err); got != tc.want {
				t.Fatalf("isBillingDisabled(%q) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// The 400 failedPrecondition billing error must be non-fatal: isPermissionDenied
// folds it in so the single skipIfDenied gate routes it (previously it escaped as
// a fatal ScanError). A generic 400 with no billing/not-enabled marker still
// returns false (stays fatal — genuine bug, not an environmental precondition).
func TestIsPermissionDeniedFoldsBilling400(t *testing.T) {
	billing400 := &googleapi.Error{Code: 400, Message: "Billing is disabled for project 317820104764."}
	if !isPermissionDenied(billing400) {
		t.Fatal("isPermissionDenied should return true for 400 billing-disabled")
	}
	generic400 := &googleapi.Error{Code: 400, Message: "Invalid value for field 'foo'."}
	if isPermissionDenied(generic400) {
		t.Fatal("isPermissionDenied should return false for a generic 400")
	}
}

// skipIfDenied routes billing-disabled to the billing sentinel, ahead of the
// api-not-enabled sentinel — so scanProject renders "(project: billing disabled)".
func TestSkipIfDeniedRoutesBillingToSentinel(t *testing.T) {
	billing := &googleapi.Error{Code: 403, Message: "Project p has billing disabled. Please enable it."}
	got := skipIfDenied(nil, "spanner:instances.list", "p", billing)
	if !errors.Is(got, errBillingDisabled) {
		t.Fatalf("skipIfDenied billing error = %v, want errBillingDisabled", got)
	}
}
