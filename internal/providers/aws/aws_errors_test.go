package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/store"
)

// alwaysRetryV2 is a stub RetryerV2 that reports every error retryable, so a test
// can observe exactly which errors withNonRetryableCodes overrides to false.
type alwaysRetryV2 struct{}

func (alwaysRetryV2) IsErrorRetryable(error) bool                  { return true }
func (alwaysRetryV2) MaxAttempts() int                             { return 3 }
func (alwaysRetryV2) RetryDelay(int, error) (time.Duration, error) { return 0, nil }
func (alwaysRetryV2) GetRetryToken(context.Context, error) (func(error) error, error) {
	return func(error) error { return nil }, nil
}
func (alwaysRetryV2) GetInitialToken() func(error) error { return func(error) error { return nil } }
func (alwaysRetryV2) GetAttemptToken(context.Context) (func(error) error, error) {
	return func(error) error { return nil }, nil
}

// v1OnlyRetryer implements the deprecated Retryer interface but NOT RetryerV2, to
// prove withNonRetryableCodes returns it unchanged rather than dropping retries.
type v1OnlyRetryer struct{}

func (v1OnlyRetryer) IsErrorRetryable(error) bool                  { return true }
func (v1OnlyRetryer) MaxAttempts() int                             { return 3 }
func (v1OnlyRetryer) RetryDelay(int, error) (time.Duration, error) { return 0, nil }
func (v1OnlyRetryer) GetRetryToken(context.Context, error) (func(error) error, error) {
	return func(error) error { return nil }, nil
}
func (v1OnlyRetryer) GetInitialToken() func(error) error { return func(error) error { return nil } }

func TestWithNonRetryableCodes(t *testing.T) {
	r := withNonRetryableCodes(alwaysRetryV2{}, "AuthorizerConfigurationException")

	if r.IsErrorRetryable(apiErr("AuthorizerConfigurationException", "Internal server error")) {
		t.Error("AuthorizerConfigurationException must be non-retryable")
	}
	// Every other error keeps the underlying (here: always-retry) behaviour, so a
	// genuine transient on the same client is not starved of retries.
	if !r.IsErrorRetryable(apiErr("ThrottlingException", "slow down")) {
		t.Error("ThrottlingException must still delegate to the wrapped retryer (retryable)")
	}
	if !r.IsErrorRetryable(errors.New("plain non-API error")) {
		t.Error("non-API error must delegate to the wrapped retryer (retryable)")
	}
	// The V2 attempt-token interface the adaptive rate limiter needs must survive.
	if _, ok := r.(sdkaws.RetryerV2); !ok {
		t.Error("wrapped retryer must still satisfy RetryerV2")
	}
}

func TestWithNonRetryableCodes_NonV2Unchanged(t *testing.T) {
	v1 := v1OnlyRetryer{}
	got := withNonRetryableCodes(v1, "AuthorizerConfigurationException")
	// A retryer that is not a RetryerV2 is returned as-is: better to keep its full
	// retry behaviour than to silently drop retries by failing to wrap.
	if _, wrapped := got.(nonRetryable); wrapped {
		t.Error("non-RetryerV2 input must be returned unwrapped")
	}
	if !got.IsErrorRetryable(apiErr("AuthorizerConfigurationException", "x")) {
		t.Error("unwrapped v1 retryer keeps its own (retryable) behaviour")
	}
}

func apiErr(code, msg string) error {
	return &smithy.GenericAPIError{Code: code, Message: msg}
}

func TestIsAccountNotInitialized(t *testing.T) {
	// DRS + MGN return this before the account is onboarded; it must route
	// through markServiceDisabled (errServiceDisabled sentinel), not a scan error.
	uninit := apiErr("UninitializedAccountException", "Account not initialized")
	if !isAccountNotInitialized(uninit) {
		t.Error("UninitializedAccountException should match")
	}
	if isAccountNotInitialized(apiErr("AccessDenied", "denied")) {
		t.Error("AccessDenied must not match")
	}
	if isAccountNotInitialized(errors.New("plain")) {
		t.Error("plain error must not match")
	}
	if !errors.Is(markServiceDisabled(uninit), errServiceDisabled) {
		t.Error("markServiceDisabled(uninit) should satisfy errors.Is(errServiceDisabled)")
	}
}

func TestMarkServiceNotEntitled(t *testing.T) {
	// Distinct sentinel from errServiceDisabled — the dispatch loop switches on
	// each separately to pick the right progress-line suffix.
	wrapped := markServiceNotEntitled(apiErr("AccessDeniedException", "Access denied due to support level"))
	if !errors.Is(wrapped, errServiceNotEntitled) {
		t.Error("markServiceNotEntitled should satisfy errors.Is(errServiceNotEntitled)")
	}
	if errors.Is(wrapped, errServiceDisabled) {
		t.Error("errServiceNotEntitled must not collide with errServiceDisabled")
	}
}

func TestIsProxyPreviewRegionGap(t *testing.T) {
	// NetworkFirewall proxy ops (public preview, us-east-2 only) return this from
	// any region where the preview isn't served — silent per-region skip.
	if !isProxyPreviewRegionGap(apiErr("InvalidRequestException", "The API being called does not exist.")) {
		t.Error("InvalidRequestException 'does not exist' should match")
	}
	// A real InvalidRequestException with a different message must not be swallowed.
	if isProxyPreviewRegionGap(apiErr("InvalidRequestException", "malformed input")) {
		t.Error("unrelated InvalidRequestException must not match")
	}
	if isProxyPreviewRegionGap(apiErr("AccessDenied", "does not exist")) {
		t.Error("wrong code must not match even with matching message")
	}
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
		"AccessDeniedException", "NotAuthorized", "NotAuthorizedException", "ForbiddenException",
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

func TestIsServiceNotAvailableInRegion(t *testing.T) {
	yes := apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")
	if !isServiceNotAvailableInRegion(yes) {
		t.Error("region-gap gateway message should be classified service-not-in-region")
	}
	// A real per-action IAM denial shares the code but names the action — must warn, not skip.
	if isServiceNotAvailableInRegion(apiErr("AccessDeniedException", "User: arn:aws:iam::1:user/x is not authorized to perform: omics:ListAnnotationStores")) {
		t.Error("real IAM denial must not be classified service-not-in-region")
	}
	if isServiceNotAvailableInRegion(apiErr("ValidationException", "Unable to determine service/operation name")) {
		t.Error("non-access-denied code with hint must not match")
	}
	// MediaLive Anywhere carries the same gateway hint under NotFoundException in
	// regions where it isn't offered — must classify as a region gap.
	if !isServiceNotAvailableInRegion(apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")) {
		t.Error("NotFoundException carrier of the region-gap hint should match")
	}
	if isServiceNotAvailableInRegion(apiErr("NotFoundException", "resource xyz not found")) {
		t.Error("unrelated NotFoundException must not match")
	}
	if isServiceNotAvailableInRegion(nil) {
		t.Error("nil error must not match")
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

func TestMarkServiceUnavailable(t *testing.T) {
	upstream := errors.New("Unable to determine service/operation name to be authorized")
	wrapped := markServiceUnavailable(upstream)
	if !errors.Is(wrapped, errServiceUnavailable) {
		t.Error("errors.Is should detect errServiceUnavailable sentinel")
	}
	if errors.Is(wrapped, errServiceDisabled) {
		t.Error("unavailable sentinel must be distinct from errServiceDisabled")
	}
	if wrapped.Error() == errServiceUnavailable.Error() {
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
	internalSvcExc := &smithy.GenericAPIError{Code: "InternalServerException", Message: "internal server exception"}
	authConfig := &smithy.GenericAPIError{Code: "AuthorizerConfigurationException", Message: "Internal server error"}
	tooManyReqs := &smithy.GenericAPIError{Code: "TooManyRequestsException", Message: "too many requests"}
	accessDenied := &smithy.GenericAPIError{Code: "AccessDenied", Message: "denied"}
	validation := &smithy.GenericAPIError{Code: "ValidationException", Message: "bad input"}

	// Transport send failure: connection dropped mid-request. Smithy wraps a bare
	// io.EOF, which is neither a net.* type nor an APIError. Observed post-retry on
	// transcribe:ListCallAnalyticsCategories.
	sendEOF := &smithyhttp.RequestSendError{Err: io.EOF}
	wrappedSendEOF := fmt.Errorf(
		"operation error Transcribe: ListCallAnalyticsCategories, exceeded maximum number of attempts, 10, %w", sendEOF)

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
		{"smithy InternalServerException", internalSvcExc, true},
		{"smithy AuthorizerConfigurationException 500", authConfig, true},
		{"smithy TooManyRequestsException", tooManyReqs, true},
		{"smithy AccessDenied not transient", accessDenied, false},
		{"smithy ValidationException not transient", validation, false},
		{"context canceled not transient", context.Canceled, false},
		{"plain errors.New not transient", errors.New("some unrelated failure"), false},
		{"request send error direct", sendEOF, true},
		{"request send error wrapped (transcribe shape)", wrappedSendEOF, true},
		{"bare io.EOF", io.EOF, true},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, true},
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

func TestHTTPStatusCode(t *testing.T) {
	respErr := func(code int) error {
		return &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: code}},
			Err:      apiErr("UnknownError", ""),
		}
	}
	if c, ok := httpStatusCode(respErr(404)); !ok || c != 404 {
		t.Errorf("httpStatusCode(404 resp) = (%d, %v), want (404, true)", c, ok)
	}
	if c, ok := httpStatusCode(fmt.Errorf("wrapped: %w", respErr(403))); !ok || c != 403 {
		t.Errorf("httpStatusCode(wrapped 403 resp) = (%d, %v), want (403, true)", c, ok)
	}
	if c, ok := httpStatusCode(apiErr("AccessDenied", "denied")); ok || c != 0 {
		t.Errorf("httpStatusCode(bare APIError) = (%d, %v), want (0, false)", c, ok)
	}
	if c, ok := httpStatusCode(nil); ok || c != 0 {
		t.Errorf("httpStatusCode(nil) = (%d, %v), want (0, false)", c, ok)
	}
}

func TestIsHTTP404(t *testing.T) {
	resp := func(code int) error {
		return &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: code}},
			Err:      apiErr("UnknownError", ""),
		}
	}
	if !isHTTP404(resp(404)) {
		t.Error("404 response should match")
	}
	if !isHTTP404(fmt.Errorf("wrapped: %w", resp(404))) {
		t.Error("wrapped 404 should match")
	}
	if isHTTP404(resp(403)) {
		t.Error("403 must not match")
	}
	if isHTTP404(apiErr("AccessDenied", "denied")) {
		t.Error("bare APIError (no transport status) must not match")
	}
	if isHTTP404(nil) {
		t.Error("nil must not match")
	}
}

func TestTranscribeRegionErr(t *testing.T) {
	// Call Analytics sub-feature gap → per-op skip (nil), Transcribe still works.
	handled, out := transcribeRegionErr(apiErr("BadRequestException", "Call Analytics isn't supported in this region."))
	if !handled || out != nil {
		t.Errorf("sub-feature gap: handled=%v out=%v; want true + nil", handled, out)
	}
	// Whole service absent in region (catalog over-reports) → mark unavailable so
	// the sibling phases short-circuit with one (region: unavailable) marker.
	handled, out = transcribeRegionErr(apiErr("BadRequestException", "Your account isn't authorized to call this operation."))
	if !handled || !errors.Is(out, errServiceUnavailable) {
		t.Errorf("service absent: handled=%v out=%v; want true + errServiceUnavailable", handled, out)
	}
	if errors.Is(out, errServiceDisabled) || errors.Is(out, errServiceNotEntitled) {
		t.Error("service-absent must be unavailable, not disabled/not-entitled")
	}
	// Unclassified shapes propagate.
	if handled, _ := transcribeRegionErr(apiErr("BadRequestException", "malformed input")); handled {
		t.Error("unrelated BadRequestException must not be handled")
	}
	if handled, _ := transcribeRegionErr(apiErr("AccessDeniedException", "isn't supported in this region")); handled {
		t.Error("wrong code must not be handled even with matching message")
	}
}

func TestMHOListErr(t *testing.T) {
	// Closed-to-new-customers → not-entitled sentinel (halts the whole service).
	handled, out := mhoListErr(apiErr("AccessDeniedException", "This service is no longer open to new customers"))
	if !handled || !errors.Is(out, errServiceNotEntitled) {
		t.Errorf("closed-to-new-customers: handled=%v out=%v; want true + errServiceNotEntitled", handled, out)
	}
	// Non-home-region rejection → region gap, swallowed to nil.
	handled, out = mhoListErr(apiErr("ValidationException", "Unauthorized access denied"))
	if !handled || out != nil {
		t.Errorf("home-region gap: handled=%v out=%v; want true + nil", handled, out)
	}
	// Anything else is unclassified — caller treats it as a real error.
	if handled, _ := mhoListErr(apiErr("ThrottlingException", "slow down")); handled {
		t.Error("unrelated error must not be handled")
	}
}

// timeoutError satisfies net.Error with Timeout()==true. Used to assert the
// net.Error+Timeout branch of the classifier independent of DNSError/OpError.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// A "Unable to determine service/operation name" denial is a region-routing gap,
// not a real IAM deny: skipIfAccessDenied returns nil AND records NO warning.
func TestSkipIfAccessDenied_RegionGapSilent(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }

	err := skipIfAccessDenied(st, "bedrockagentcore", "123456789012", "eu-west-1",
		apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized"))
	if err != nil {
		t.Errorf("region gap: returned %v, want nil", err)
	}
	if warned {
		t.Error("region gap must not record a warning")
	}
	// A genuine denial still warns.
	warned = false
	_ = skipIfAccessDenied(st, "iam", "123456789012", "us-east-1", apiErr("AccessDenied", "denied"))
	if !warned {
		t.Error("real denial must still warn")
	}
}

func TestTranslateBlockListed(t *testing.T) {
	if !translateBlockListed(apiErr("NotAuthorizedException",
		"Your account has been block-listed for access to Amazon Translate. Please contact AWS Support")) {
		t.Error("block-listed message must match")
	}
	if translateBlockListed(apiErr("NotAuthorizedException", "some other reason")) {
		t.Error("unrelated NotAuthorized message must not match")
	}
	if translateBlockListed(apiErr("AccessDenied", "block-listed")) {
		t.Error("only NotAuthorizedException carries the block-list; other codes must not match")
	}
}

func TestConnectCampaignsNotProvisioned(t *testing.T) {
	if !connectCampaignsNotProvisioned(apiErr("AccessDeniedException",
		"User: arn:aws:sts::1:assumed-role/r is not authorized to perform: null")) {
		t.Error("null-action denial (no Connect instance) must match")
	}
	if !connectCampaignsNotProvisioned(apiErr("ForbiddenException", "Forbidden")) {
		t.Error("bare ForbiddenException:Forbidden must match")
	}
	if connectCampaignsNotProvisioned(apiErr("AccessDeniedException",
		"is not authorized to perform: connect-campaigns:ListCampaigns")) {
		t.Error("a real action-identifying denial must NOT be silenced")
	}
}

func TestChimeListErr_AvailabilityNoiseSilent(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }

	for _, msg := range []string{"This feature is not available", "AWS account is not enabled", ""} {
		warned = false
		chimeListErr(st, "chime:ListVoiceProfileDomains", "1", "eu-west-1", apiErr("ForbiddenException", msg))
		if warned {
			t.Errorf("ForbiddenException %q is availability noise; must not warn", msg)
		}
	}
	// A message-bearing IAM denial still warns.
	warned = false
	chimeListErr(st, "chime:ListVoiceProfileDomains", "1", "eu-west-1",
		apiErr("AccessDeniedException", "User: arn:... is not authorized to perform: chime:ListVoiceProfileDomains"))
	if !warned {
		t.Error("real IAM denial must warn")
	}
}

func TestBedrockDAListErr(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }

	// Region feature gap → silent nil.
	if err := bedrockDAListErr(st, "op", "1", "us-east-2", apiErr("AccessDeniedException", "library feature is not supported in this region")); err != nil || warned {
		t.Errorf("region gap: err=%v warned=%v; want nil + no warn", err, warned)
	}
	// Empty-body denial = whole service absent in region → unavailable sentinel
	// (short-circuits to one (region: unavailable) suffix), no warning.
	warned = false
	if err := bedrockDAListErr(st, "op", "1", "sa-east-1", apiErr("AccessDeniedException", "")); !errors.Is(err, errServiceUnavailable) || warned {
		t.Errorf("empty-body: err=%v warned=%v; want errServiceUnavailable + no warn", err, warned)
	}
	// A message-bearing denial still warns (nil, but recorded).
	warned = false
	if err := bedrockDAListErr(st, "op", "1", "us-east-1", apiErr("AccessDeniedException", "User: arn is not authorized to perform: bedrock:ListBlueprints")); err != nil || !warned {
		t.Errorf("real denial: err=%v warned=%v; want nil + warn", err, warned)
	}
	// Unrelated error propagates.
	if err := bedrockDAListErr(st, "op", "1", "us-east-1", apiErr("ThrottlingException", "slow down")); err == nil {
		t.Error("unrelated error must propagate")
	}
}
