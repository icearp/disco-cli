package gcp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/googleapi"
)

// errServiceDisabled is a sentinel returned by per-service scanners when the
// GCP API itself is not enabled in the calling project. scanProject's
// dispatch loop detects it via errors.Is and surfaces "(project: disabled)"
// on the per-service progress line — no warning, no error report. Wrap
// upstream errors via markServiceDisabled so the original message survives
// for debugging if unwrapped. Mirrors the AWS pattern in
// internal/providers/aws/aws_errors.go.
var errServiceDisabled = errors.New("gcp service not enabled")

// markServiceDisabled wraps the upstream "API not enabled" error so the
// dispatch loop can identify it via errors.Is(err, errServiceDisabled).
// skipIfDenied returns this when isAPINotEnabled matches.
func markServiceDisabled(err error) error {
	return fmt.Errorf("%w: %s", errServiceDisabled, err.Error())
}

// errBillingDisabled is a sentinel returned when the calling project has
// billing disabled (free trial ended / no billing account). scanProject
// detects it via errors.Is and surfaces "(project: billing disabled)" on the
// per-service progress line — no warning, no error. Billing is self-enableable
// (associate a billing account), so it sits in the annotation family alongside
// errServiceDisabled rather than the warnings block. GCP is inconsistent about
// the HTTP code: some services return 403 "...has billing disabled...", others
// 400 failedPrecondition "Billing is disabled for project ..." — isBillingDisabled
// matches on message, so both flavours land here.
var errBillingDisabled = errors.New("gcp project billing disabled")

// markBillingDisabled wraps the upstream billing error so the dispatch loop can
// identify it via errors.Is(err, errBillingDisabled). skipIfDenied returns this
// when isBillingDisabled matches.
func markBillingDisabled(err error) error {
	return fmt.Errorf("%w: %s", errBillingDisabled, err.Error())
}

// isBillingDisabled reports whether err is a GCP "billing disabled" precondition.
// Matches on message (code-agnostic) because GCP returns it as both 403
// ("Project ... has billing disabled.") and 400 failedPrecondition
// ("Billing is disabled for project ...").
func isBillingDisabled(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	msg := strings.ToLower(gerr.Message)
	return strings.Contains(msg, "billing is disabled") || strings.Contains(msg, "has billing disabled")
}

// isAPINotEnabled is a narrow predicate that matches the three known shapes
// GCP uses to signal "this API is not enabled in the project":
//   - 403 with message "...has not been used in project..." (most APIs)
//   - 400 with message "has not enabled..." (BigQuery, Spanner billing-flavour)
//   - error reason "accessNotConfigured" inside googleapi.Error.Errors[]
//
// Distinct from isPermissionDenied (wider — also fires on real IAM 403s);
// the two disagree on some inputs. isAPINotEnabled gates the sentinel path,
// isPermissionDenied the warning path.
func isAPINotEnabled(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	if strings.Contains(gerr.Message, "has not been used in project") {
		return true
	}
	if strings.Contains(gerr.Message, "has not enabled") {
		return true
	}
	for _, e := range gerr.Errors {
		if e.Reason == "accessNotConfigured" {
			return true
		}
	}
	return false
}

// isPermissionDenied reports whether err is a GCP 403 / permission denied error.
//
// Also covers the BigQuery quirk where API-not-enabled surfaces as HTTP 400
// with message "has not enabled BigQuery" instead of the usual 403
// `accessNotConfigured`. Treating both as non-fatal lets downstream code use
// a single `skipIfDenied` path for the "service unreachable in this project"
// failure mode regardless of which HTTP code the API picks.
func isPermissionDenied(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		if gerr.Code == http.StatusForbidden || gerr.Code == http.StatusUnauthorized {
			return true
		}
		if gerr.Code == http.StatusBadRequest && strings.Contains(gerr.Message, "has not enabled") {
			return true
		}
	}
	// Billing-disabled arrives as 400 failedPrecondition on some services, which
	// no code check above catches. Fold it in so the single skipIfDenied gate
	// (~130 call sites all keyed on isPermissionDenied) routes it non-fatally;
	// skipIfDenied then re-classifies to the billing sentinel.
	if isBillingDisabled(err) {
		return true
	}
	return false
}

// skipIfDenied either escalates the error to the service-disabled sentinel
// (when isAPINotEnabled matches — see errServiceDisabled) or records it as
// a ScanWarning and returns nil. The sentinel path keeps the disabled-API
// case off the warnings block; only real permission denials warn.
func skipIfDenied(st *store.Store, service, projectID string, err error) error {
	if isBillingDisabled(err) {
		return markBillingDisabled(err)
	}
	if isAPINotEnabled(err) {
		return markServiceDisabled(err)
	}
	msg := err.Error()
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		reason := ""
		if len(gerr.Errors) > 0 {
			reason = gerr.Errors[0].Reason
		}
		if reason != "" {
			msg = fmt.Sprintf("%d %s (%s)", gerr.Code, gerr.Message, reason)
		} else {
			msg = fmt.Sprintf("%d %s", gerr.Code, gerr.Message)
		}
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "gcp",
		Service:  service,
		Scope:    projectID,
		Message:  msg,
	})
	return nil
}
