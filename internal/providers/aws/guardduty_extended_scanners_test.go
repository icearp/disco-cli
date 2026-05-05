package aws

import (
	"errors"
	"testing"

	"github.com/aws/smithy-go"
)

func TestIsGuardDutyMemberRestricted(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "member-restricted bad request",
			err: &smithy.GenericAPIError{
				Code:    "BadRequestException",
				Message: "The request is rejected because member accounts cannot manage specified resources or properties.",
			},
			want: true,
		},
		{
			name: "vanilla bad request",
			err:  &smithy.GenericAPIError{Code: "BadRequestException", Message: "Validation failed"},
			want: false,
		},
		{
			name: "access denied",
			err:  &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: ... not authorized"},
			want: false,
		},
		{
			name: "non-api error",
			err:  errors.New("network unreachable"),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGuardDutyMemberRestricted(tc.err); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
