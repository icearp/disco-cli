package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	smithy "github.com/aws/smithy-go"
)

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
	"AccessDeniedException", "NotAuthorized", "ForbiddenException",
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

// errServiceDisabled is a sentinel returned by per-service scanners when they
// detect that the AWS service itself is not enabled in the calling account or
// region (Macie not enabled, Shield Advanced not subscribed, Security Hub
// hub not present, etc). The scanRegion / scanAccount dispatch loop detects
// it via errors.Is and surfaces "(service disabled)" on the progress line —
// no warning, no error report. Wrap upstream errors via markServiceDisabled
// so the original message is preserved for debugging if anyone unwraps.
var errServiceDisabled = errors.New("aws service not enabled")

// markServiceDisabled wraps the upstream "feature not enabled" error so the
// dispatch loop can identify it via errors.Is(err, errServiceDisabled). Per-
// service helpers (isShieldNotSubscribed, isSecurityHubNotEnabled, Macie's
// "Macie is not enabled" message check) return this from their phase-1
// detection step instead of nil.
func markServiceDisabled(err error) error {
	return fmt.Errorf("%w: %s", errServiceDisabled, err.Error())
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
	return isAPIErrorCode(err,
		"RequestTimeout", "RequestTimeoutException", "RequestCanceled",
		"ServiceUnavailable", "ServiceUnavailableException",
		"InternalServerError", "InternalFailure", "InternalServerErrorException",
		// Post-retry throttling exhaust: SDK retryer already burned its budget,
		// the error reaching us is a momentary TPS pressure spike. Treat as
		// transient so siblings continue rather than aborting the region.
		"ThrottlingException", "Throttling", "ThrottledException", "RateExceededException")
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
