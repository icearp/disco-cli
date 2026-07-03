package gcp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/googleapi"
)

// errServiceDisabled is a sentinel returned by per-service scanners when
// the GCP API itself is not enabled in the calling project. The scanProject
// dispatch loop detects it via errors.Is and surfaces "(project: disabled)"
// on the per-service progress line — no warning, no error report. Wrap
// upstream errors via markServiceDisabled so the original message is
// preserved for debugging if anyone unwraps. Mirrors the AWS pattern in
// internal/providers/aws/aws_errors.go.
var errServiceDisabled = errors.New("gcp service not enabled")

// markServiceDisabled wraps the upstream "API not enabled" error so the
// dispatch loop can identify it via errors.Is(err, errServiceDisabled).
// skipIfDenied returns this when isAPINotEnabled matches.
func markServiceDisabled(err error) error {
	return fmt.Errorf("%w: %s", errServiceDisabled, err.Error())
}

// isAPINotEnabled is a narrow predicate that matches the three known shapes
// GCP uses to signal "this API is not enabled in the project":
//   - 403 with message "...has not been used in project..." (most APIs)
//   - 400 with message "has not enabled..." (BigQuery, Spanner billing-flavour)
//   - error reason "accessNotConfigured" inside googleapi.Error.Errors[]
//
// Distinct from isPermissionDenied (a wider check that also fires on real
// IAM 403s); the two don't agree on every input. isAPINotEnabled is what
// gates the sentinel path; isPermissionDenied gates the warning path.
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
	return false
}

// skipIfDenied either escalates the error to the service-disabled sentinel
// (when isAPINotEnabled matches — see errServiceDisabled) or records it as
// a ScanWarning and returns nil. The sentinel path keeps the disabled-API
// case off the warnings block; only real permission denials warn.
func skipIfDenied(st *store.Store, service, projectID string, err error) error {
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
