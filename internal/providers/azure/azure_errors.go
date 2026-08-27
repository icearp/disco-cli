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
	"unicode"

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

// isAuthenticationFailure reports whether err is an Azure 401 response error.
//
// A 401 is not a permission gap. It says the presented token has no standing
// for the resource at all -- under Azure Lighthouse, that the subscription was
// never delegated to this principal, or that the delegation is gone. That is
// distinct from a 403, where the principal IS recognised and only the role is
// too narrow for the one call.
//
// This does NOT change how a 401 is treated in general: isAccessDenied covers
// 401 and 403 alike, so isSkippableScanError still absorbs both into a
// per-call warning at every site. [subscriptionUnreachable] is the single
// caller that asks a 401 a different question, and only about the
// resource-provider probe.
func isAuthenticationFailure(err error) bool {
	var respErr *azcore.ResponseError
	// errors.As MATCHES a typed nil and sets the target to it, so the != nil is
	// not redundant with the As -- without it a wrapped (*ResponseError)(nil)
	// dereferences here. readARMErrorMessage carries the same guard. The
	// package has older unguarded sites; these are the ones this gate reaches.
	if errors.As(err, &respErr) && respErr != nil {
		return respErr.StatusCode == http.StatusUnauthorized
	}
	return false
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
	// Ahead of the redactor, which would destroy it: this message is redacted at
	// construction. See [unreachableSubscription].
	var unreachable *unreachableSubscription
	if errors.As(err, &unreachable) {
		return unreachable.msg
	}
	// Ahead of everything else: a credential failure carries disco's own
	// identifiers and reaches more than one arm below, including the two that
	// return err.Error() verbatim.
	if red := redactCredentialError(err); red != "" {
		return red
	}
	var respErr *azcore.ResponseError
	// respErr == nil is not covered by the As: it MATCHES a typed nil and sets
	// the target to it. See scanBodyForAADSTS for the set and how to re-derive it.
	if !errors.As(err, &respErr) || respErr == nil {
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
// The `respErr.StatusCode == http.StatusUnauthorized` disjunct is UNREACHABLE
// from today's sole caller: redactCredentialError answers every
// *azcore.ResponseError 401 in its own first branch and returns before this
// runs. It is kept because the predicate's contract is about the error, not
// about that caller's ordering. The *graphErr 401 arm below is NOT dead --
// graphErr carries no Unwrap, so it never satisfies errors.As for
// *azcore.ResponseError and never meets that first branch.
func scanBodyForAADSTS(err error) bool {
	var respErr *azcore.ResponseError
	// The != nil is load-bearing: errors.As MATCHES a typed nil and sets the
	// target to it. A review enumerated three sites needing this; the test
	// (TestAuthPredicates_SurviveATypedNilResponseError) then found two more,
	// this one and formatAzureError. Older sites in this file are still
	// unguarded; isAccessDenied is one the gate's own goroutine does reach,
	// harmlessly, since reportPanic wraps it. Re-derive the set across the
	// PACKAGE -- `grep -rn 'errors.As(err, &respErr)' internal/providers/azure/`,
	// which finds two more outside this file -- never from a list.
	if errors.As(err, &respErr) && respErr != nil {
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

// subscriptionUnreachable reports whether a resource-provider probe failure
// means the whole subscription must be refused rather than scanned.
//
// A conjunction, not a status check. rgListed says the resource-group list
// succeeded, which proves the token WAS accepted for this subscription, so a
// 401 from the providers endpoint alone is some other failure and refusing the
// subscription on it would cost the customer every row they can actually see.
//
// A nil probeErr needs no separate arm: errors.As over a nil error is false, so
// isAuthenticationFailure already answers false for it.
func subscriptionUnreachable(probeErr error, rgListed bool) bool {
	return isAuthenticationFailure(probeErr) && !rgListed
}

// unreachableSubscriptionError renders the scan-record message for a
// subscription whose RP-registration probe answered 401. Apart from whatever
// the resource-group goroutine already put on the record before the gate can
// fire, it is the only thing written about that subscription. Deliberately not
// enumerated -- three rounds of review each found the previous enumeration short
// by one. Read scanResourceGroups and skipIfAccessDenied for the set.
//
// It carries the ARM error CODE and drops the body, for the reason
// [redactCredentialError] states: the body of an
// InvalidAuthenticationTokenTenant names the issuer that WAS presented, which
// under federation is disco's own directory, and that GUID is identical for
// every customer. The scan record is the customer's.
//
// redactCredentialError WOULD catch this -- its first net is every 401 -- and
// the two are deliberately separate anyway: that one answers "the token was
// rejected", this one is the REFUSAL of a whole subscription and says so.
// Neither is redundant, but NOT because of the code: both render it, through
// the same [armAuthCode], and that sharing is deliberate. The difference is the
// DECISION each reports -- redactCredentialError describes one call that was
// refused, this refuses the whole subscription and is the only sentence written
// about it. Nor does the code separate a delegation that never existed from one
// that was withdrawn: ARM answers InvalidAuthenticationTokenTenant for both,
// which is exactly why the message below hedges instead of asserting a cause.
//
// It names the causes as POSSIBILITIES, and names them at all because the gate
// suppresses the message that used to. scanSubscriptionResource warns "it may
// not exist, or its delegation to this identity may have been revoked" when a
// subscription is absent from the tenant list, and it is a per-subscription
// SERVICE -- the gate returns before the service phase runs, so refusing the
// subscription would otherwise have taken a strictly more actionable sentence
// off the record with it. Hedged rather than asserted: the conjunction the gate
// tests cannot rule out a transient 401 both calls share, which scanSubscription
// documents as reachable (an in-flight tenant transfer), and for that scan none
// of the three named causes is true.
//
// The operator reads a full 401 body for this subscription on stderr, keyed per
// subscription -- normally the resource-group list's rather than this one's,
// since it runs inside preWG and claims the shared key first.
func unreachableSubscriptionError(err error) error {
	msg := err.Error()
	code := "Unauthorized"
	var respErr *azcore.ResponseError
	// No respErr != nil here: armAuthCode nil-checks its own parameter, so this
	// guard could not prevent a panic and would only withhold an AADSTS code
	// the wrapper text carries.
	if errors.As(err, &respErr) {
		code = armAuthCode(respErr, msg)
	}
	logRawCredentialError(credentialLogKey(code, msg), msg)
	return &unreachableSubscription{msg: fmt.Sprintf(
		"subscription unreachable: ARM refused this token for the subscription (401 %s) -- "+
			"the delegation to this identity may never have been made, may have been revoked, "+
			"or the subscription may not exist", code)}
}

// unreachableSubscription carries the gate's message through the shared
// formatter unchanged. [formatAzureError] answers it before anything else.
//
// It is a TYPE rather than a plain error because formatAzureError re-formats
// whatever scanSubscription returns, and redactCredentialError runs first. The
// gate names its diagnostic code inline, so the moment [armAuthCode] began
// preferring an AADSTS code the sentence contained the literal "AADSTS" --
// which is precisely what scanBodyForAADSTS's bare text fallback tests, since a
// plain fmt.Errorf satisfies neither errors.As arm above it. The formatter then
// collapsed the whole sentence into the generic "azure authentication failed
// (AADSTS700213); see scanner logs" and logged the gate's own prose to stderr
// as a "credential failure", claiming that code's dedupe key and suppressing a
// later genuine failure's body for the life of the process.
//
// The general shape: a redactor run over an ALREADY-REDACTED message can only
// destroy it, and it destroys the more specific one. Anything constructed
// pre-redacted needs a way past the shared formatter.
type unreachableSubscription struct{ msg string }

func (e *unreachableSubscription) Error() string { return e.msg }

// armAuthCode picks the diagnostic code a 401 renders onto the scan record. It
// is shared by [redactCredentialError] and [unreachableSubscriptionError] so
// the two cannot disagree about which code a given error carries.
//
// An AADSTS code found in the message OUTRANKS respErr.ErrorCode:
// InvalidAuthenticationToken is generic and AADSTS700213 names the actual
// fault, while carrying no identifier itself. The gate path took ErrorCode
// alone before this was shared, so an ARM 401 with an empty ErrorCode and an
// AADSTS token in the body rendered the specific code through the formatter and
// the bare Unauthorized literal through the gate -- losing the diagnostic on
// exactly the path that is the only thing written about that subscription.
//
// Returns the literal Unauthorized when neither is present. ARM can answer 401
// with no error code at all, and the parenthetical then rendered as a bare "()",
// which reads like a bug in disco rather than a refusal by Azure.
func armAuthCode(respErr *azcore.ResponseError, msg string) string {
	code := ""
	if respErr != nil {
		code = respErr.ErrorCode
	}
	if aad := aadstsCodeRE.FindString(msg); aad != "" {
		code = aad
	}
	if code = boundedCode(code); code == "" {
		return "Unauthorized"
	}
	return code
}

// boundedCode caps and printable-filters an ARM error code for rendering onto
// the scan record.
//
// ErrorCode is not a fixed vocabulary: azcore reads it from the x-ms-error-code
// RESPONSE HEADER when present and from the body's `code` field otherwise, so
// its length and contents are the remote's choice.
//
// The invariant, rather than a count that two rounds of review got wrong: remote
// text this package splices into a hand-built sentence is bounded either HERE
// (the two 401 sentences, via armAuthCode and the AADSTS arm) or at
// sanitizeForScanRecord (everything reportEntra and reportPanic report).
// formatAzureError's *ResponseError arms are the GAP -- they render ErrorCode
// and the ARM message verbatim and nothing downstream caps them. A real ARM
// code is a short identifier well under the cap, so the cap is invisible in
// practice and bounds only the pathological case.
func boundedCode(s string) string {
	const max = 64
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return '\uFFFD'
	}, s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "\u2026"
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
	// A 401 is ARM refusing OUR token, and its body says why in terms of OUR
	// identity: InvalidAuthenticationTokenTenant quotes the issuer that was
	// presented, which under Lighthouse federation is disco's own directory and
	// is identical for every customer. The scan record it would land on is the
	// customer's. The CODE is the whole diagnosis and carries none of it.
	//
	// The STATUS is the test, not a list of codes -- the same reasoning the 401
	// bullet above gives for scanBodyForAADSTS, and for the same reason: a code
	// list is a hand-maintained allow-list on a disclosure boundary and Microsoft
	// adds codes. Keying this on an `InvalidAuthenticationToken` prefix was the
	// first version, and it left ExpiredAuthenticationToken, AuthenticationFailed
	// and InvalidAuthenticationInfo storing their bodies verbatim.
	// AuthorizationFailed, the one error that must NOT be redacted, is a 403 and
	// is untouched.
	//
	// This sits in the SHARED formatter rather than at the one gate that refuses
	// the subscription, because the gate is not the only caller: on the same scan
	// the resource-group list answers the same 401 and reports it through
	// skipIfAccessDenied, which stored the body verbatim. Closing only the gate
	// left that copy live.
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) && respErr != nil && respErr.StatusCode == http.StatusUnauthorized {
		// Keyed per SUBSCRIPTION, not per code and not per body: the code alone
		// prints the first subscription's cause and silently drops every other
		// one on a multi-subscription scan, while the body alone never dedupes.
		// [armAuthCode] is what keeps this sentence and the gate's agreeing about
		// which code the error carries.
		msg := err.Error()
		// Keyed on the code that is RENDERED, not respErr.ErrorCode: two 401s on
		// one subscription with an empty ErrorCode and DIFFERENT AADSTS codes
		// otherwise collapse onto one key and the second cause is never printed
		// -- the exact failure this key exists to prevent.
		logRawCredentialError(credentialLogKey(armAuthCode(respErr, msg), msg), msg)
		return "azure token rejected for this scope (" + armAuthCode(respErr, msg) + "); see scanner logs"
	}
	var authErr *azidentity.AuthenticationFailedError
	if !errors.As(err, &authErr) && !scanBodyForAADSTS(err) {
		return ""
	}
	msg := err.Error()
	// Bounded for the same reason the ARM code is: `AADSTS\d+` has no length
	// limit, and this splices the match straight into a scan-record message.
	code := boundedCode(aadstsCodeRE.FindString(msg))
	logRawCredentialError(code, msg)
	if code != "" {
		return "azure authentication failed (" + code + "); see scanner logs"
	}
	return "azure authentication failed; see scanner logs"
}

// loggedCredentialErrors dedupes the stderr copy of a redacted cause, keyed by
// whatever the caller passes to [logRawCredentialError] -- a diagnostic code
// alone for a CONFIGURATION fault, or the code plus the subscription the fault
// belongs to ([credentialLogKey]).
//
// The whole message is the wrong key and looks like the right one: an Entra
// failure body carries a Trace ID, a Correlation ID and a timestamp, all unique
// per call, so keying on it never dedupes anything and accumulates multi-KB keys
// for the life of the process. A code ALONE is the wrong key in the other
// direction wherever the body varies per subscription, which is why the key is
// a parameter rather than derived here.
var loggedCredentialErrors sync.Map

// logRawCredentialError writes the unredacted cause where the OPERATOR can
// read it — the scan record only gets the code, and a cause nobody can recover
// is not a redaction but a deletion.
//
// This is the one place a provider writes to stderr: everything else in this
// package reports through the store, and the store is exactly the sink the
// message must not reach. Deduped because a credential failure repeats per
// service, and for a CONFIGURATION fault the text is a property of the config
// rather than of any one call — the same reasoning that keeps a static
// condition off a per-request log line. That is why the key is a parameter:
// ARM's token-rejection body is a property of the SUBSCRIPTION instead, and a
// code-only key there would print the first one and silently drop the rest.
func logRawCredentialError(code, msg string) {
	key := code
	if key == "" {
		// No code to key on. Fall back to a bounded prefix rather than the
		// whole body, which is unique per call.
		key = boundedKey(msg)
	}
	if _, seen := loggedCredentialErrors.LoadOrStore(key, struct{}{}); seen {
		return
	}
	fmt.Fprintln(os.Stderr, "azure: credential failure (redacted on the scan record): "+msg)
}

// subscriptionPathRE matches the /subscriptions/<guid> segment of an ARM
// request line, which is the part of a 401 body that makes it a property of one
// SUBSCRIPTION rather than of one request.
var subscriptionPathRE = regexp.MustCompile(`/subscriptions/[0-9a-fA-F-]{36}`)

// credentialLogKey derives the dedupe key for the stderr copy of a 401 whose
// cause belongs to one subscription.
//
// The subscription segment is what it keys on, because a bounded PREFIX of the
// message does not do the job the comments around it claim: ResponseError's
// first line is `METHOD scheme://host EscapedPath`, so a 120-byte window runs
// past the subscription GUID and into the resource-provider namespace, making
// the key per REQUEST PATH. That was tolerable while only an AADSTS-bearing 401
// reached here; the wider 401 net means every ARM 401 does, so a subscription
// whose resource-group list succeeds and whose every service 401s would print
// one stderr line per distinct ARM path.
//
// Falls back to the bounded prefix when the message names no subscription --
// in practice a TENANT-scoped ARM call, whose path carries none. Both callers
// are gated on an *azcore.ResponseError 401, so a credential failure raised
// before any request is built never reaches here; the fallback is defensive
// against that shape, not written for it. Those are back to per-request-path keying.
// For the management-group listing that is harmless, since it is issued once
// per scan -- but GET /subscriptions is NOT: it is
// registered with registerService, so it runs once per SUBSCRIPTION over one
// tenant-wide API, and all N of its 401s share a request line. They collapse
// onto ONE key and N-1 bodies are dropped, which is the failure this function
// exists to prevent, reappearing in its own fallback. Tolerated only because
// those N bodies are near-identical.
func credentialLogKey(code, msg string) string {
	if sub := subscriptionPathRE.FindString(msg); sub != "" {
		return code + "|" + sub
	}
	return code + "|" + boundedKey(msg)
}

// boundedKey caps a dedupe key so one entry of loggedCredentialErrors cannot
// hold a multi-KB body. It bounds the SIZE of an entry and not the NUMBER of
// them -- a message that varies inside the window still mints a key per call,
// which is why [credentialLogKey] keys on the subscription instead. Callers
// that key on an error's text rather than its code use it.
func boundedKey(s string) string {
	if len(s) > 120 {
		return s[:120]
	}
	return s
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
