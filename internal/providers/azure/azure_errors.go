package azure

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// isAccessDenied reports whether err is an Azure 403/401 response error.
func isAccessDenied(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusForbidden ||
			respErr.StatusCode == http.StatusUnauthorized
	}
	return false
}

// isSubscriptionNotRegistered reports the benign "resource provider not
// registered on this subscription" condition (404 SubscriptionNotRegistered /
// 409 MissingSubscriptionRegistration) — there can be no resources of the
// type, so the list call is skipped like AccessDenied.
func isSubscriptionNotRegistered(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.ErrorCode == "SubscriptionNotRegistered" ||
			respErr.ErrorCode == "MissingSubscriptionRegistration"
	}
	return false
}

// isResourceTypeUnavailable reports the benign "this resource type / API
// version is not available in this scope" condition. It fires when an SDK
// module pins an API version newer than what the target tenant/region has
// rolled out (404 InvalidResourceType: "the resource type X could not be found
// in the namespace Y for api version Z") or when the pinned API version is not
// accepted at all (400 InvalidApiVersionParameter / NoRegisteredProviderFound).
// Like an unregistered provider, there is nothing to enumerate, so the list is
// skipped rather than surfaced as a hard error.
func isResourceTypeUnavailable(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.ErrorCode == "InvalidResourceType" ||
			respErr.ErrorCode == "InvalidApiVersionParameter" ||
			respErr.ErrorCode == "NoRegisteredProviderFound"
	}
	return false
}

// isSkippableScanError is the canonical "this list call cannot return
// resources; log a ScanWarning and continue" predicate for scanners. An
// unregistered resource provider (isSubscriptionNotRegistered) or an
// unavailable resource type / API version (isResourceTypeUnavailable) genuinely
// has no resources to enumerate, exactly like a denied (isAccessDenied) list.
func isSkippableScanError(err error) bool {
	return isAccessDenied(err) || isSubscriptionNotRegistered(err) || isResourceTypeUnavailable(err)
}

// isFeatureNotAvailable reports whether err is a 400 FeatureDisabledOnSelectedEdition
// or similar "not supported on this edition/tier" error. These are expected when
// scanning databases on editions that don't support certain features (e.g.
// workload groups require Business Critical or Premium; ledger requires certain tiers).
func isFeatureNotAvailable(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusBadRequest &&
			(respErr.ErrorCode == "FeatureDisabledOnSelectedEdition" ||
				respErr.ErrorCode == "FeatureNotSupported" ||
				respErr.ErrorCode == "UnsupportedEdition")
	}
	return false
}

// skipIfAccessDenied reports a non-fatal skip as a ScanWarning.
func skipIfAccessDenied(st *store.Store, service, subID string, err error) error {
	st.ReportWarning(store.ScanWarning{
		Provider: "azure",
		Service:  service,
		Scope:    subID,
		Message:  formatAzureError(err),
	})
	return nil
}

// formatAzureError narrows an Azure SDK error to a single concise line:
// `"{statusCode} {errorCode}: {message}"`. Mirrors the GCP `skipIfDenied`
// shape so end-of-scan warnings/errors render uniformly across providers.
//
// `azcore.ResponseError.Error()` dumps the full HTTP request+response
// (preamble + body) which is multi-KB per warning. We use the SDK's already-
// parsed `ErrorCode` + `StatusCode` and, when present, the ARM `error.message`
// field from the response body. Falls back to `err.Error()` when:
//   - err is not an `*azcore.ResponseError` (e.g. store / JSON / I/O errors)
//   - response body is missing or unparseable
//
// Body read is best-effort: any failure returns the status+code only.
func formatAzureError(err error) string {
	if err == nil {
		return ""
	}
	var respErr *azcore.ResponseError
	if !errors.As(err, &respErr) {
		return err.Error()
	}
	code := respErr.ErrorCode
	if code == "" && respErr.StatusCode > 0 {
		code = http.StatusText(respErr.StatusCode)
	}
	msg := readARMErrorMessage(respErr)
	switch {
	case respErr.StatusCode > 0 && msg != "":
		return fmt.Sprintf("%d %s: %s", respErr.StatusCode, code, msg)
	case respErr.StatusCode > 0:
		return fmt.Sprintf("%d %s", respErr.StatusCode, code)
	case msg != "":
		return fmt.Sprintf("%s: %s", code, msg)
	default:
		return err.Error()
	}
}

// readARMErrorMessage extracts `{"error":{"message":"..."}}` from the SDK's
// buffered response body. Returns "" on any failure path.
func readARMErrorMessage(respErr *azcore.ResponseError) string {
	if respErr == nil || respErr.RawResponse == nil || respErr.RawResponse.Body == nil {
		return ""
	}
	body, err := io.ReadAll(respErr.RawResponse.Body)
	if err != nil || len(body) == 0 {
		return ""
	}
	var arm struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &arm) != nil {
		return ""
	}
	return arm.Error.Message
}
