package aws

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// TestIsSecurityLakeNotEnabled pins the three phrasings AWS uses for one
// account state, and the denials that must keep warning.
//
// The account-state cases are not hypothetical: with Security Lake off
// everywhere, ListSubscribers answered the delegated-administrator phrasing in
// us-east-1 / us-east-2 / eu-west-1 / ap-northeast-1 and
// ResourceNotFoundException in eu-west-2. Only the access-denied variants were
// matched, so that single region turned an account-level fact into a hard scan
// error and marked every scan partial.
func TestIsSecurityLakeNotEnabled(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// The eu-west-2 shape. A ResourceNotFoundException, not an access
		// denial, which is why the access-denied check cannot gate every branch.
		{
			"not enabled in any region (ResourceNotFoundException)",
			apiErr("ResourceNotFoundException", "The request failed because Security Lake isn't enabled for your account in any Regions. Enable Security Lake for your account and then try again."),
			true,
		},
		{
			// The full shape the SDK hands the scanner: the typed exception inside
			// a 404 transport response, inside the scanner's own fmt.Errorf wrap.
			"the same, as the SDK actually delivers it",
			fmt.Errorf("securitylake:ListSubscribers: %w", &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
				Err:      apiErr("ResourceNotFoundException", "The request failed because Security Lake isn't enabled for your account in any Regions."),
			}),
			true,
		},

		// The two access-denied phrasings, unchanged by this fix.
		{"delegated administrator prerequisite", apiErr("AccessDeniedException", "To perform this operation, your account must be a delegated Security Lake administrator account or a standalone Security Lake account."), true},
		{"never onboarded", apiErr("AccessDeniedException", "Your account is not authorized to perform this operation"), true},
		{
			// Wrapped the way the SDK actually ships it, for the same reason the
			// ResourceNotFoundException case above is: a helper reading err.Error()
			// instead of the typed message passes the bare case and fails this one.
			"delegated administrator prerequisite, wrapped",
			fmt.Errorf("securitylake:ListDataLakes: %w", &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 403}},
				Err:      apiErr("AccessDeniedException", "To perform this operation, your account must be a delegated Security Lake administrator account."),
			}),
			true,
		},

		// The negative half.
		{
			"real IAM denial names the action and must still warn",
			apiErr("AccessDeniedException", "User: arn:aws:iam::1:role/scanner is not authorized to perform: securitylake:ListSubscribers"),
			false,
		},
		{
			// ResourceNotFoundException is a common AWS code. Matching it on the
			// code alone would silently swallow a genuine missing-resource error,
			// which is a real gap the operator needs to see.
			"unrelated ResourceNotFoundException",
			apiErr("ResourceNotFoundException", "Subscriber not found: sub-123"),
			false,
		},
		{"unrelated error", errors.New("boom"), false},
		{"nil", nil, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSecurityLakeNotEnabled(c.err); got != c.want {
				t.Errorf("isSecurityLakeNotEnabled(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
