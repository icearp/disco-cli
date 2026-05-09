package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"codeberg.org/icearp/disco/store"
	smithy "github.com/aws/smithy-go"
)

func apiErr(code, msg string) error {
	return &smithy.GenericAPIError{Code: code, Message: msg}
}

func TestIsAPIErrorCode(t *testing.T) {
	if !isAPIErrorCode(apiErr("AccessDenied", ""), "AccessDenied") {
		t.Error("expected match")
	}
	if isAPIErrorCode(apiErr("Other", ""), "AccessDenied") {
		t.Error("expected no match")
	}
	if isAPIErrorCode(errors.New("plain error"), "AccessDenied") {
		t.Error("plain error should not match")
	}
	if isAPIErrorCode(nil, "AccessDenied") {
		t.Error("nil error should not match")
	}
}

func TestIsAccessDenied(t *testing.T) {
	codes := []string{
		"AccessDenied", "UnauthorizedOperation", "AuthFailure",
		"AccessDeniedException", "NotAuthorized", "ForbiddenException",
	}
	for _, c := range codes {
		if !isAccessDenied(apiErr(c, "")) {
			t.Errorf("isAccessDenied(%s) = false, want true", c)
		}
	}
	if isAccessDenied(apiErr("ValidationException", "")) {
		t.Error("ValidationException should not be access-denied")
	}
	if isAccessDenied(nil) {
		t.Error("nil should not be access-denied")
	}
}

func TestIsAPIErrorWithMessage(t *testing.T) {
	if !isAPIErrorWithMessage(apiErr("ValidationException", "Feature not supported yet"), "ValidationException", "Feature not supported") {
		t.Error("expected code+needle match")
	}
	if isAPIErrorWithMessage(apiErr("ValidationException", "other body"), "ValidationException", "Feature not supported") {
		t.Error("needle mismatch must not match")
	}
	if isAPIErrorWithMessage(apiErr("AccessDeniedException", "Feature not supported"), "ValidationException", "Feature not supported") {
		t.Error("code mismatch must not match")
	}
	if isAPIErrorWithMessage(errors.New("plain error"), "ValidationException", "x") {
		t.Error("plain error must not match")
	}
	if isAPIErrorWithMessage(nil, "ValidationException", "x") {
		t.Error("nil must not match")
	}
}

func TestIsAccessDeniedWithMessage(t *testing.T) {
	for _, c := range accessDeniedCodes {
		if !isAccessDeniedWithMessage(apiErr(c, "Macie is not enabled"), "Macie is not enabled") {
			t.Errorf("%s + needle should match", c)
		}
	}
	if isAccessDeniedWithMessage(apiErr("AccessDeniedException", "user is not authorized"), "Macie is not enabled") {
		t.Error("needle mismatch must not match")
	}
	if isAccessDeniedWithMessage(apiErr("ValidationException", "Macie is not enabled"), "Macie is not enabled") {
		t.Error("non-access-denied code must not match")
	}
	if isAccessDeniedWithMessage(nil, "x") {
		t.Error("nil must not match")
	}
}

func TestIsAuditManagerNotEnabled(t *testing.T) {
	yes := apiErr("AccessDeniedException", "Please complete AWS Audit Manager setup from the home page")
	if !isAuditManagerNotEnabled(yes) {
		t.Error("setup-hint message should be classified not-enabled")
	}
	plainDeny := apiErr("AccessDeniedException", "user is not authorized")
	if isAuditManagerNotEnabled(plainDeny) {
		t.Error("plain access-denied must not be classified not-enabled")
	}
}

func TestIsMacieNotEnabled(t *testing.T) {
	yes := apiErr("AccessDeniedException", "Macie is not enabled")
	if !isMacieNotEnabled(yes) {
		t.Error("'Macie is not enabled' should be classified not-enabled")
	}
	if isMacieNotEnabled(apiErr("AccessDeniedException", "user is not authorized")) {
		t.Error("plain access-denied must not be classified not-enabled")
	}
	if isMacieNotEnabled(apiErr("ValidationException", "Macie is not enabled")) {
		t.Error("non-AD code with hint must not match")
	}
}

func TestIsSecurityHubNotEnabled(t *testing.T) {
	for _, c := range []string{"InvalidAccessException", "ResourceNotFoundException"} {
		if !isSecurityHubNotEnabled(apiErr(c, "")) {
			t.Errorf("%s should classify SecurityHub not-enabled", c)
		}
	}
	if isSecurityHubNotEnabled(apiErr("AccessDeniedException", "")) {
		t.Error("plain AccessDenied must not classify SecurityHub not-enabled")
	}
}

func TestIsShieldNotSubscribed(t *testing.T) {
	if !isShieldNotSubscribed(apiErr("ResourceNotFoundException", "")) {
		t.Error("ResourceNotFoundException should classify Shield not-subscribed")
	}
	if isShieldNotSubscribed(apiErr("AccessDeniedException", "")) {
		t.Error("AccessDenied must not classify Shield not-subscribed")
	}
}

func TestIsControlTowerNotEnabled(t *testing.T) {
	hints := []string{
		"AWSControlTowerAdmin role missing", "not the management account",
		"landing zone is not configured", "AWS Control Tower has not been deployed",
	}
	for _, msg := range hints {
		if !isControlTowerNotEnabled(apiErr("AccessDeniedException", msg)) {
			t.Errorf("AD + %q should classify CT not-enabled", msg)
		}
		if !isControlTowerNotEnabled(apiErr("ValidationException", msg)) {
			t.Errorf("ValidationException + %q should classify CT not-enabled", msg)
		}
	}
	if isControlTowerNotEnabled(apiErr("AccessDeniedException", "user not authorized")) {
		t.Error("AD without CT hint must not classify")
	}
	if isControlTowerNotEnabled(apiErr("ValidationException", "bad input parameter")) {
		t.Error("plain ValidationException must not classify")
	}
	if isControlTowerNotEnabled(apiErr("ThrottlingException", "AWSControlTowerAdmin")) {
		t.Error("non-AD non-Validation code with hint must not classify")
	}
}

func TestIsCacheSecurityGroupsNotPermitted(t *testing.T) {
	yes := apiErr("InvalidParameterValue",
		"Use of cache security groups is not permitted in this API version for your account.")
	if !isCacheSecurityGroupsNotPermitted(yes) {
		t.Error("expected match")
	}
	if isCacheSecurityGroupsNotPermitted(apiErr("InvalidParameterValue", "different message")) {
		t.Error("message mismatch must not classify")
	}
	if isCacheSecurityGroupsNotPermitted(apiErr("AccessDeniedException",
		"Use of cache security groups is not permitted in this API version for your account.")) {
		t.Error("wrong code must not classify")
	}
}

func TestMarkServiceDisabled(t *testing.T) {
	upstream := errors.New("Macie is not enabled")
	wrapped := markServiceDisabled(upstream)
	if !errors.Is(wrapped, errServiceDisabled) {
		t.Error("errors.Is should detect errServiceDisabled sentinel")
	}
	if !errors.Is(wrapped, errServiceDisabled) || wrapped.Error() == errServiceDisabled.Error() {
		t.Error("wrapped error should preserve upstream message")
	}
}

func TestSkipIfAccessDenied_RecordsWarningReturnsNil(t *testing.T) {
	st := newTestStore(t)
	var got store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { got = w }

	err := skipIfAccessDenied(st, "iam", "123456789012", "us-east-1", apiErr("AccessDenied", "denied"))
	if err != nil {
		t.Errorf("skipIfAccessDenied returned %v, want nil", err)
	}
	if got.Service != "iam" || got.Provider != "aws" || got.Scope != "123456789012/us-east-1" {
		t.Errorf("warning fields: %+v", got)
	}
	if got.Message != "api error AccessDenied: denied" {
		t.Errorf("message = %q", got.Message)
	}
}

func TestSkipIfAccessDenied_GlobalScope(t *testing.T) {
	st := newTestStore(t)
	var got store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { got = w }

	_ = skipIfAccessDenied(st, "iam", "123456789012", "global", apiErr("AccessDenied", "x"))
	if got.Scope != "123456789012" {
		t.Errorf("global region must collapse to acct only; got %q", got.Scope)
	}

	_ = skipIfAccessDenied(st, "iam", "123456789012", "", apiErr("AccessDenied", "x"))
	if got.Scope != "123456789012" {
		t.Errorf("empty region must collapse to acct only; got %q", got.Scope)
	}
}

func TestSkipIfTransient_RecordsWarningReturnsNil(t *testing.T) {
	st := newTestStore(t)
	var got store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { got = w }

	err := skipIfTransient(st, "ec2", "123456789012", "us-east-1", apiErr("RequestTimeout", "timeout"))
	if err != nil {
		t.Errorf("skipIfTransient returned %v, want nil", err)
	}
	if got.Service != "ec2" || got.Scope != "123456789012/us-east-1" {
		t.Errorf("warning fields: %+v", got)
	}
}

// TestIsTransientNetworkError covers positive + negative classifier cases.
// The guard exists because one DNS blip during DescribeAlarms otherwise
// aborts the entire scan.
func TestIsTransientNetworkError(t *testing.T) {
	dns := &net.DNSError{Err: "no such host", Name: "monitoring.us-east-1.amazonaws.com", IsNotFound: true}
	opDial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	opRead := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("broken pipe")}
	timeoutErr := &timeoutError{}
	wrappedDNS := fmt.Errorf("operation error CloudWatch: DescribeAlarms, %w", dns)

	reqTimeout := &smithy.GenericAPIError{Code: "RequestTimeout", Message: "request timed out"}
	svcUnavail := &smithy.GenericAPIError{Code: "ServiceUnavailable", Message: "service unavailable"}
	internal := &smithy.GenericAPIError{Code: "InternalFailure", Message: "internal failure"}
	accessDenied := &smithy.GenericAPIError{Code: "AccessDenied", Message: "denied"}
	validation := &smithy.GenericAPIError{Code: "ValidationException", Message: "bad input"}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"dns error direct", dns, true},
		{"dns error wrapped", wrappedDNS, true},
		{"op dial error", opDial, true},
		{"op read error", opRead, true},
		{"net timeout", timeoutErr, true},
		{"smithy RequestTimeout", reqTimeout, true},
		{"smithy ServiceUnavailable", svcUnavail, true},
		{"smithy InternalFailure", internal, true},
		{"smithy AccessDenied not transient", accessDenied, false},
		{"smithy ValidationException not transient", validation, false},
		{"context canceled not transient", context.Canceled, false},
		{"plain errors.New not transient", errors.New("some unrelated failure"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isTransientNetworkError(c.err)
			if got != c.want {
				t.Errorf("isTransientNetworkError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// timeoutError satisfies net.Error with Timeout()==true. Used to assert the
// net.Error+Timeout branch of the classifier independent of DNSError/OpError.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
