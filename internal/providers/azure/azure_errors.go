package azure

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/icearp/disco-cli/store"
)

// errServiceNotRegistered is a sentinel returned (instead of a scan warning)
// when a service isn't available in this subscription — the resource provider
// isn't registered, or ARM reports its resource type as absent. The dispatch
// loop detects it via errors.Is and reports the service as disabled — the
// Azure analog of AWS's errServiceDisabled. Since each Azure service maps 1:1
// to an ARM namespace, an unavailable RP disables the whole service.
var errServiceNotRegistered = errors.New("azure resource provider not available in subscription")

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
		if respErr.ErrorCode == "SubscriptionNotRegistered" ||
			respErr.ErrorCode == "MissingSubscriptionRegistration" {
			return true
		}
		// Some RPs (e.g. Microsoft.Maintenance) report an unregistered
		// subscription as a bare 400 with no error code — the phrase lives only
		// in the response body. respErr.Error() is built and cached at
		// construction (azcore NewResponseErrorWithErrorCode), so this is a
		// cheap, idempotent string check that never touches the body.
		return strings.Contains(strings.ToLower(respErr.Error()), "subscription not registered")
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

// isUnsupportedOperation reports a bare 404 (empty ErrorCode) on a list call —
// the shape Azure returns when an operation is not available at the requested
// scope (e.g. a sub-wide List the RP only supports per-resource: "404 Operation
// not supported"). There is nothing to enumerate, so it is benign. Kept narrow
// to an empty ErrorCode: coded 404s (ResourceGroupNotFound, ResourceNotFound,
// …) are handled by their own predicates or deliberately surfaced, and the
// specific api-version 404s are matched by isResourceTypeUnavailable.
func isUnsupportedOperation(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == http.StatusNotFound && respErr.ErrorCode == ""
	}
	return false
}

// isDeserializationError reports an encoding/json decode failure surfaced by an
// SDK pager — either a malformed body (*json.SyntaxError, e.g. a BOM-prefixed
// response that armpeering PeerAsns returns) or a field-type mismatch
// (*json.UnmarshalTypeError, e.g. armnetwork VirtualApplianceSKUs returning
// instanceCount as a string where the generated struct types it int32). These
// are SDK-vs-live-API mismatches: disco can't extract data but must not
// hard-fail the scan. Safe to skip because azSimpleScan compile-checks the
// response type against the pager, so a decode error is always external, never
// a wrong-type bug on our side. azcore formats the json error with %s (not %w),
// so errors.As can't reach it — the string fallback below handles the real
// pager errors; the errors.As checks cover any properly-wrapped variants.
func isDeserializationError(err error) bool {
	if err == nil {
		return false
	}
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
		return true
	}
	// azcore's runtime.UnmarshalAsJSON formats the json error with %s, not %w
	// (azcore runtime/response.go), so errors.As cannot reach the underlying
	// *json.SyntaxError / *json.UnmarshalTypeError — e.g. armpeering PeerAsns
	// returns a BOM-prefixed body that fails to parse. Fall back to azcore's
	// stable "unmarshalling type <T>:" signature.
	return strings.Contains(err.Error(), "unmarshalling type ")
}

// mentionsSupportedAPIVersions reports whether an InvalidResourceType error's
// message lists "supported api-versions" — the ARM signal that the RP IS
// registered but the requested api-version isn't served yet (disco's SDK is
// ahead of rollout). Its absence means the RP/type is simply not present in
// this subscription. Reads the cached respErr.Error() (built at construction),
// so it never disturbs the response body.
func mentionsSupportedAPIVersions(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return strings.Contains(strings.ToLower(respErr.Error()), "supported api-versions")
	}
	return false
}

// isProviderUnavailable reports the "service not available in this
// subscription" condition that should render as a disabled service rather than
// a warning: an unregistered RP (isSubscriptionNotRegistered), or a resource
// type/api-version ARM reports as simply absent (isResourceTypeUnavailable with
// no "supported api-versions" list — which would instead mean the RP is
// registered but disco's SDK is ahead of rollout, a warnable skew).
func isProviderUnavailable(err error) bool {
	if isSubscriptionNotRegistered(err) {
		return true
	}
	return isResourceTypeUnavailable(err) && !mentionsSupportedAPIVersions(err)
}

// isSkippableScanError is the canonical "this list call cannot return
// resources; log a ScanWarning and continue" predicate for scanners. An
// unregistered resource provider (isSubscriptionNotRegistered), an unavailable
// resource type / API version (isResourceTypeUnavailable), an operation
// unsupported at this scope (isUnsupportedOperation), or an unparseable SDK
// response (isDeserializationError) genuinely yields no enumerable resources,
// exactly like a denied (isAccessDenied) list.
func isSkippableScanError(err error) bool {
	return isAccessDenied(err) ||
		isSubscriptionNotRegistered(err) ||
		isResourceTypeUnavailable(err) ||
		isUnsupportedOperation(err) ||
		isDeserializationError(err)
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
	// Service unavailable in this subscription (RP not registered, or its
	// resource type absent) mirrors AWS's "service disabled" case: signal the
	// dispatch loop to mark it disabled (no warning, no error) instead of
	// emitting noise for a service the sub doesn't use.
	if isProviderUnavailable(err) {
		return errServiceNotRegistered
	}
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
// `azcore.ResponseError.Error()` renders the request line (method, scheme,
// host, escaped path — no headers, no query), the response status and the full
// body, which is multi-KB per warning. What this saves is what gets STORED,
// not the render: azcore builds that string at construction whether we read it
// or not. We use the SDK's already-
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
	// Ahead of everything else: a credential failure carries disco's own
	// identifiers and reaches more than one arm below, including the two that
	// return err.Error() verbatim.
	if red := redactCredentialError(err); red != "" {
		return red
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

// aadstsCodeRE matches the diagnostic code Entra puts at the head of an
// authentication failure.
var aadstsCodeRE = regexp.MustCompile(`AADSTS\d+`)

// scanBodyForAADSTS reports whether err's text should be searched for an
// AADSTS code.
//
// Not a cost gate, though the shape invites that reading. An
// *azcore.ResponseError has ALREADY rendered itself by the time we see one:
// runtime.NewResponseError passes respErr.Error() to log.Write as an argument,
// so the render happens unconditionally at construction and memoizes into an
// unexported field. There is no dump left to avoid.
//
// It is a FALSE-POSITIVE gate. Redaction throws the message away, so
// collapsing an ordinary ARM failure that merely mentions AADSTS in its body
// would cost the customer the one diagnostic that is genuinely theirs — the
// action and scope they lack permission on. So an ARM error is answered from
// what ARM itself classified it as, and only three shapes get past the code to
// the text:
//
//   - an AADSTS code parsed straight into ErrorCode (which needs no search —
//     the code is already the answer);
//   - a 401, which is ARM rejecting OUR token rather than the customer's
//     permissions, and which carries the AADSTS text in the MESSAGE where the
//     code cannot show it (InvalidAuthenticationToken and its siblings). The
//     status is the test rather than a list of codes: a list is a
//     hand-maintained allow-list on a disclosure boundary, and Microsoft adds
//     codes. AuthorizationFailed — the one error that must NOT be redacted —
//     is a 403;
//   - no parsed code at all (an HTML proxy page, say), where there is nothing
//     else to test and "no code" must not be read as "not a credential
//     failure".
//
// Graph gets one shape rather than the ARM arm's three: status 401 AND an
// AADSTS mention. There is no ErrorCode analogue to answer from — graphErr
// parses nothing, it holds the status and the raw body — so the status is the
// whole classification and the substring is what it classifies.
//
// It has something real to protect: the body relays Entra's own text, which
// names a tenant in exactly the AADSTS prose this redaction exists for (see
// TestReportEntraErr_RedactsAGraphTokenRejection). What a graphErr cannot
// carry is a URL, since it is by construction a graph.microsoft.com response;
// the failures carrying an authority URL or an AWS ARN arrive from
// graphClient.get before a request is built, as azidentity errors caught by
// the type arm above.
//
// The arm exists because reportEntraErr routes through formatAzureError:
// without it a Graph error fell to the bare substring test below, and a 403
// saying Authorization_RequestDenied — the customer's missing consent, theirs
// to act on — was collapsed if the body mentioned AADSTS.
func scanBodyForAADSTS(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		switch {
		case strings.Contains(respErr.ErrorCode, "AADSTS"):
			return true
		case respErr.ErrorCode == "" || respErr.StatusCode == http.StatusUnauthorized:
			return strings.Contains(err.Error(), "AADSTS")
		default:
			return false
		}
	}
	var gErr *graphErr
	if errors.As(err, &gErr) {
		return gErr.status == http.StatusUnauthorized && strings.Contains(err.Error(), "AADSTS")
	}
	return strings.Contains(err.Error(), "AADSTS")
}

// redactCredentialError collapses a credential failure to its diagnostic code,
// returning "" for anything that is not one.
//
// Under workload identity federation this text names disco's own
// infrastructure: azidentity prints the token authority URL — which carries
// disco's tenant GUID — before any response body, and an assertion-callback
// failure carries STS's "User: arn:aws:sts::<disco account>:assumed-role/..."
// instead of a body at all. Neither string is a secret, but this one is stored
// on the CUSTOMER's scan record, and disco's identifiers are not part of what
// they scanned for.
//
// Triggered on the error TYPE rather than on the AADSTS substring, because the
// cases that leak most carry no AADSTS code: a 429 or 502 with an HTML body, a
// TLS or DNS failure, and every AWS-side failure. Every one of those reaches
// the caller as an AuthenticationFailedError, because the STS call happens
// inside the assertion callback and azidentity wraps whatever it returns.
// [scanBodyForAADSTS] is the second, narrower net, for a credential failure
// that arrives as some other type.
//
// Deliberately NOT triggered on the "azure wif:" prefix, though that reads like
// the obvious net. Every error raised before the exchange carries it too — a
// half-set contract, a missing AWS_REGION — and those name a variable the
// operator must fix. Collapsing them to "authentication failed" is the exact
// outcome the eager region check exists to prevent, so the wide net would have
// cancelled two other guards in this same change.
//
// The code is lifted out when present — it is the whole diagnostic value and
// identifies nothing. The operator reads the rest in the scanner's own logs.
//
// Unconditional, not gated on federation. A standalone operator's own
// DefaultAzureCredential failure is collapsed the same way, which loses them
// nothing they cannot recover — logRawCredentialError puts the full cause on
// stderr, which for them is the same terminal. Threading the federation
// contract this far down to vary a message was not worth the coupling.
func redactCredentialError(err error) string {
	if err == nil {
		return ""
	}
	var authErr *azidentity.AuthenticationFailedError
	if !errors.As(err, &authErr) && !scanBodyForAADSTS(err) {
		return ""
	}
	msg := err.Error()
	code := aadstsCodeRE.FindString(msg)
	logRawCredentialError(code, msg)
	if code != "" {
		return "azure authentication failed (" + code + "); see scanner logs"
	}
	return "azure authentication failed; see scanner logs"
}

// loggedCredentialErrors dedupes the stderr copy of a redacted cause, keyed by
// diagnostic code rather than by message.
//
// The message is the wrong key and looks like the right one: an Entra failure
// body carries a Trace ID, a Correlation ID and a timestamp, all unique per
// call, so keying on it never dedupes anything and accumulates multi-KB keys
// for the life of the process. The code is what actually repeats.
var loggedCredentialErrors sync.Map

// logRawCredentialError writes the unredacted cause where the OPERATOR can
// read it — the scan record only gets the code, and a cause nobody can recover
// is not a redaction but a deletion.
//
// This is the one place a provider writes to stderr: everything else in this
// package reports through the store, and the store is exactly the sink the
// message must not reach. Deduped because a credential failure repeats per
// service, and the text is a property of the configuration rather than of any
// one call — the same reasoning that keeps a static condition off a per-request
// log line.
func logRawCredentialError(code, msg string) {
	key := code
	if key == "" {
		// No code to key on. Fall back to a bounded prefix rather than the
		// whole body, which is unique per call.
		key = msg
		if len(key) > 120 {
			key = key[:120]
		}
	}
	if _, seen := loggedCredentialErrors.LoadOrStore(key, struct{}{}); seen {
		return
	}
	fmt.Fprintln(os.Stderr, "azure: credential failure (redacted on the scan record): "+msg)
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
