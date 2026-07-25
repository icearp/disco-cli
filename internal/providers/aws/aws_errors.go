package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	smithy "github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/store"
)

// nonRetryable wraps a [sdkaws.RetryerV2] to force the given API error codes to
// be treated as non-retryable, preserving every other retry behaviour (adaptive
// backoff, throttling, transient 5xx) and the V2 attempt-token interface the
// adaptive rate limiter depends on. It exists for permanent per-region gaps that
// AWS signals with a retryable status — e.g. AgentCore Control's HTTP 500
// AuthorizerConfigurationException in regions where the service front-end
// resolves but is not provisioned — so one structural gap cannot burn a
// service's whole retry budget on doomed attempts.
type nonRetryable struct {
	sdkaws.RetryerV2
	codes map[string]struct{}
}

// IsErrorRetryable reports whether err should be retried, returning false for any
// of the wrapped codes and otherwise deferring to the embedded retryer.
func (r nonRetryable) IsErrorRetryable(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		if _, ok := r.codes[ae.ErrorCode()]; ok {
			return false
		}
	}
	return r.RetryerV2.IsErrorRetryable(err)
}

// withNonRetryableCodes wraps r so the named API error codes are treated as
// non-retryable. It returns r unchanged if r is not a [sdkaws.RetryerV2] (the
// standard and adaptive retry modes both are), so an unexpected retryer type
// degrades to the default behaviour rather than dropping retries entirely.
func withNonRetryableCodes(r sdkaws.Retryer, codes ...string) sdkaws.Retryer {
	rv2, ok := r.(sdkaws.RetryerV2)
	if !ok {
		return r
	}
	m := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		m[c] = struct{}{}
	}
	return nonRetryable{RetryerV2: rv2, codes: m}
}

// isAPIErrorCode reports whether err is a Smithy APIError whose ErrorCode()
// matches any of the given codes. Single choke point for AWS error-code
// predicates in this package — use directly for one-off checks, or wrap in
// a named helper (see isAccessDenied) for codes reused across many sites.
func isAPIErrorCode(err error, codes ...string) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	return slices.Contains(codes, ae.ErrorCode())
}

// isAccessDenied reports whether err is an AWS permission error. Such errors
// are expected when the scanning role lacks access to a specific service or
// region and should be logged then skipped rather than aborting the scan.
func isAccessDenied(err error) bool {
	return isAPIErrorCode(err, accessDeniedCodes...)
}

// accessDeniedCodes lists every Smithy error code AWS uses for permission
// denials. Shared by isAccessDenied and isAccessDeniedWithMessage so the two
// helpers stay in sync — adding a new code in one site updates both.
var accessDeniedCodes = []string{
	"AccessDenied", "UnauthorizedOperation", "AuthFailure",
	"AccessDeniedException", "NotAuthorized", "NotAuthorizedException", "ForbiddenException",
}

// isAPIErrorWithMessage reports whether err is a Smithy APIError whose
// ErrorCode equals code and whose ErrorMessage contains needle. Use for
// AWS exception codes reused across semantically-distinct cases
// (AccessDeniedException for closed-to-customers vs real IAM deny;
// ValidationException for per-region feature gap vs malformed input) —
// the message body is the only signal that disambiguates them.
//
// Reads ae.ErrorMessage() directly rather than err.Error(), so the match
// is decoupled from the Smithy "api error CODE: MSG" wrapper format and
// from outer SDK "operation error <Op>: ..." wrapping.
func isAPIErrorWithMessage(err error, code, needle string) bool {
	var ae smithy.APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.ErrorCode() == code && strings.Contains(ae.ErrorMessage(), needle)
}

// isAccessDeniedWithMessage is the message-disambiguated form of
// isAccessDenied. Returns true when err is any of the access-denied codes
// AND ae.ErrorMessage() contains needle. Used to separate closed-to-customers
// / not-enabled-here / per-region-feature-gap denials from real IAM denies
// that share the same error code.
func isAccessDeniedWithMessage(err error, needle string) bool {
	for _, c := range accessDeniedCodes {
		if isAPIErrorWithMessage(err, c, needle) {
			return true
		}
	}
	return false
}

// isServiceNotAvailableInRegion reports whether err is the gateway-level
// "Unable to determine service/operation name to be authorized" AccessDenied —
// returned when a service's regional endpoint resolves but the service is not
// actually offered in that region (e.g. HealthOmics outside its supported
// regions, Lambda capacity-providers, Bedrock AgentCore). A per-region feature
// gap, not an IAM denial: silent-skip the call.
func isServiceNotAvailableInRegion(err error) bool {
	return isAccessDeniedWithMessage(err, "Unable to determine service/operation name") ||
		isAPIErrorWithMessage(err, "NotFoundException", "Unable to determine service/operation name")
}

// isPayerAccountOnly reports whether err is the restriction AWS returns when a
// payer/management-account-only billing API (BCM Pricing Calculator, BCM Data
// Exports, Invoicing) is called from an organisation member account. It
// surfaces as a ValidationException whose body reads "Operation not permitted
// for member accounts. This API is only allowed for regular or payer accounts."
// This reflects account topology (a member account can't self-enable the API —
// only the payer can), not a scanner misconfig, so callers mark it
// not-entitled via markServiceNotEntitled → (account: not entitled).
// Distinct from isBillingConductorPayerOnly, which matches the
// AccessDeniedException variant Billing Conductor returns.
func isPayerAccountOnly(err error) bool {
	return isAPIErrorWithMessage(err, "ValidationException", "only allowed for regular or payer accounts") ||
		isAPIErrorWithMessage(err, "ValidationException", "not permitted for member accounts")
}

// isAccountNotInitialized reports the account-level "service never initialized"
// state DRS and MGN return before first use (UninitializedAccountException) —
// the account has not been onboarded to Elastic Disaster Recovery / Application
// Migration Service. Callers route it through markServiceDisabled so the
// progress line reads (account: disabled) instead of surfacing a scan error.
func isAccountNotInitialized(err error) bool {
	return isAPIErrorCode(err, "UninitializedAccountException")
}

// errServiceDisabled is a sentinel returned by per-service scanners when they
// detect that the AWS service itself is not enabled in the calling account
// (Macie not enabled, Shield Advanced not subscribed, Security Hub hub not
// present, etc) — an account-level state the user could turn on. The
// scanRegion / scanAccount dispatch loop detects it via errors.Is and surfaces
// "(account: disabled)" on the progress line — no warning, no error report.
// Wrap upstream errors via markServiceDisabled so the original message is
// preserved for debugging if anyone unwraps. Distinct from errServiceUnavailable
// (service not deployed in this region).
var errServiceDisabled = errors.New("aws service not enabled")

// markServiceDisabled wraps the upstream "feature not enabled" error so the
// dispatch loop can identify it via errors.Is(err, errServiceDisabled). Per-
// service helpers (isShieldNotSubscribed, isSecurityHubNotEnabled, Macie's
// "Macie is not enabled" message check) return this from their phase-1
// detection step instead of nil.
func markServiceDisabled(err error) error {
	return fmt.Errorf("%w: %s", errServiceDisabled, err.Error())
}

// errServiceNotEntitled is a sentinel returned by per-service scanners when the
// service exists but the calling account is not entitled to it and the user
// cannot self-enable it — a support-tier gate (Trusted Advisor API needs
// Business/Enterprise AWS Support), a service closed to new customers (Migration
// Hub), or an account
// AWS has not made eligible (CloudSearch, "contact AWS Support"). The dispatch
// loop detects it via errors.Is and surfaces "(account: not entitled)"
// on the progress line — no warning, no error report. Distinct from
// errServiceDisabled, which the user COULD turn on.
var errServiceNotEntitled = errors.New("aws service: account not entitled")

// markServiceNotEntitled wraps the upstream not-entitled error so the dispatch
// loop can identify it via errors.Is(err, errServiceNotEntitled).
func markServiceNotEntitled(err error) error {
	return fmt.Errorf("%w: %s", errServiceNotEntitled, err.Error())
}

// errServiceUnavailable is a sentinel returned by per-service scanners when the
// WHOLE service is not deployed in the scanned region — the regional endpoint
// resolves but every op fails (e.g. HealthOmics outside its supported regions,
// where the gateway answers "Unable to determine service/operation name"). The
// dispatch loop detects it via errors.Is and surfaces "(region: unavailable)"
// on the progress line — no warning. Use only when the entire service is absent;
// a per-op / sub-feature region gap (the parent service is present) keeps its
// own silent per-phase skip instead. Distinct from errServiceDisabled
// (account-level not-enabled).
var errServiceUnavailable = errors.New("aws service not available in region")

// markServiceUnavailable wraps the upstream region-gap error so the dispatch
// loop can identify it via errors.Is(err, errServiceUnavailable).
func markServiceUnavailable(err error) error {
	return fmt.Errorf("%w: %s", errServiceUnavailable, err.Error())
}

// isSCPExplicitDeny matches the AccessDeniedException AWS returns when an
// op is blocked by an organisation service-control-policy. The body always
// carries "explicit deny in a service control policy" alongside the action.
// SCPs reflect environment policy, not a misconfig of the scanned account,
// so these denials silent-skip rather than warn. Applies to any service —
// SCPs can target any AWS action.
func isSCPExplicitDeny(err error) bool {
	return isAccessDeniedWithMessage(err, "explicit deny in a service control policy")
}

// isClosedToNewCustomers reports whether err is an AWS account-level
// "service closed to new customers" denial — surfaced as
// AccessDeniedException with an empty message body. Real per-action IAM
// denials always identify the action in the message; the empty-message
// variant is the closed-state signal. Precedent surfaces: Amazon Fraud
// Detector, IoT FleetWise, Rekognition Custom Labels.
func isClosedToNewCustomers(err error) bool {
	if !isAccessDenied(err) {
		return false
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return strings.TrimSpace(ae.ErrorMessage()) == ""
	}
	return false
}

// skipIfAccessDenied records the error as a scan warning and returns nil,
// allowing the caller to continue scanning other services. Warnings are
// collected by the orchestrator (cmd/scan.go) and rendered as a grouped
// block after the scan completes — no inline log line interleaves with
// the aligned progress output. The caller must already have verified the
// error is an access-denied shape via isAccessDenied.
func skipIfAccessDenied(st *store.Store, service, accountID, region string, err error) error {
	// "Unable to determine service/operation name" is the gateway's signal that
	// the op isn't routed in this region — a region gap, never a real IAM denial.
	// The caller already gated on isAccessDenied; silent-skip without a warning.
	if isServiceNotAvailableInRegion(err) {
		return nil
	}
	scope := accountID
	if region != "" && region != "global" {
		scope = accountID + "/" + region
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  service,
		Scope:    scope,
		Message:  err.Error(),
	})
	return nil
}

// isDNSNotFound reports whether err is an NXDOMAIN — the AWS endpoint host
// for this service+region does not exist. This is a permanent fact about
// region availability, not a transient outage. Real DNS server problems
// surface as timeouts / SERVFAIL, not NXDOMAIN. Used to silent-skip
// per-region service-not-deployed cases without recording a warning.
func isDNSNotFound(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsNotFound
	}
	return false
}

// isTransientNetworkError reports whether err looks like a momentary network
// or service-side glitch rather than a permanent failure. The SDK v2 retryer
// already handles throttling and 5xx within its budget; anything that reaches
// us is post-retry. A one-off DNS blip or endpoint flap should warn-skip and
// let sibling services continue, matching the AccessDenied policy — otherwise
// a single hiccup aborts the whole scan.
//
// Context cancellation (Ctrl-C, parent timeout) is deliberately NOT treated
// as transient: those indicate the scan should stop.
func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// Transport-level send failure: the connection dropped mid-request rather
	// than the server answering. Smithy surfaces it as *RequestSendError wrapping
	// a bare io.EOF ("request send failed, Post ...: EOF") — neither a net.* type
	// nor an APIError, so none of the checks above or below match it. Observed
	// post-retry on transcribe:ListCallAnalyticsCategories; a dropped connection
	// is a momentary glitch, so warn-skip like any other transient.
	var sendErr *smithyhttp.RequestSendError
	if errors.As(err, &sendErr) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	return isAPIErrorCode(err,
		"RequestTimeout", "RequestTimeoutException", "RequestCanceled",
		"ServiceUnavailable", "ServiceUnavailableException",
		"InternalServerError", "InternalServerException", "InternalFailure", "InternalServerErrorException",
		// Bedrock AgentCore gateway returns a 500 AuthorizerConfigurationException
		// when its authorizer backend is momentarily unhealthy; the SDK already
		// burned its retry budget by the time it reaches us. Server-side 5xx →
		// warn+continue, don't count it as a scan error.
		"AuthorizerConfigurationException",
		// Post-retry throttling exhaust: SDK retryer already burned its budget,
		// the error reaching us is a momentary TPS pressure spike. Treat as
		// transient so siblings continue rather than aborting the region.
		"ThrottlingException", "Throttling", "ThrottledException", "RateExceededException",
		"TooManyRequestsException")
}

// httpStatusCode extracts the HTTP status from a Smithy transport response error.
// Some AWS services (S3 Control, IoT data-plane) return error bodies the SDK
// cannot map to a typed exception — the Smithy code surfaces as the generic
// "UnknownError" and only the HTTP status is reliable. Returns (0, false) when
// err carries no transport response (e.g. a plain APIError or a network error).
func httpStatusCode(err error) (int, bool) {
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		return re.HTTPStatusCode(), true
	}
	return 0, false
}

// isHTTP404 reports whether err carries an HTTP 404. Several services signal a
// per-region operation gap this way — an untyped body the SDK can't map to a
// typed exception (IoT data-plane "No method found matching route"; Bedrock
// model ops returning an HTML 404 page that fails JSON decode) where only the
// transport status is reliable.
func isHTTP404(err error) bool {
	c, ok := httpStatusCode(err)
	return ok && c == 404
}

// skipIfTransient mirrors skipIfAccessDenied for transient network/service
// errors. Records a ScanWarning and returns nil so the caller can continue.
// The caller must have verified the error shape via isTransientNetworkError.
func skipIfTransient(st *store.Store, service, accountID, region string, err error) error {
	scope := accountID
	if region != "" && region != "global" {
		scope = accountID + "/" + region
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  service,
		Scope:    scope,
		Message:  "transient: " + err.Error(),
	})
	return nil
}
