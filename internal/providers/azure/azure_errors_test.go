package azure

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// TestFormatAzureError covers the three branches of formatAzureError:
// (1) azcore.ResponseError with parseable ARM body → narrow shape;
// (2) ResponseError with unparseable body → status+code only;
// (3) non-ResponseError → fallback to err.Error().
//
// Mirrors the GCP `skipIfDenied` test pattern and ensures Azure scan
// warnings render at AWS/GCP brevity.
func TestFormatAzureError(t *testing.T) {
	t.Run("ResponseError with parseable ARM body", func(t *testing.T) {
		body := `{"error":{"code":"AuthorizationFailed","message":"The client 'x' does not have permission to perform action 'Microsoft.Compute/virtualMachines/read' on resource '/subscriptions/y/providers/Microsoft.Compute/virtualMachines/z' or the scope is invalid."}}`
		respErr := &azcore.ResponseError{
			ErrorCode:  "AuthorizationFailed",
			StatusCode: http.StatusForbidden,
			RawResponse: &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(body)),
			},
		}
		got := formatAzureError(respErr)
		want := "403 AuthorizationFailed: The client 'x' does not have permission to perform action 'Microsoft.Compute/virtualMachines/read' on resource '/subscriptions/y/providers/Microsoft.Compute/virtualMachines/z' or the scope is invalid."
		if got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})

	t.Run("ResponseError with unparseable body", func(t *testing.T) {
		respErr := &azcore.ResponseError{
			ErrorCode:  "InternalError",
			StatusCode: http.StatusInternalServerError,
			RawResponse: &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("not-json-html-page")),
			},
		}
		got := formatAzureError(respErr)
		want := "500 InternalError"
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("ResponseError with empty body", func(t *testing.T) {
		respErr := &azcore.ResponseError{
			ErrorCode:  "ResourceNotFound",
			StatusCode: http.StatusNotFound,
			RawResponse: &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("")),
			},
		}
		got := formatAzureError(respErr)
		want := "404 ResourceNotFound"
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("ResponseError missing ErrorCode falls back to status text", func(t *testing.T) {
		respErr := &azcore.ResponseError{
			StatusCode: http.StatusForbidden,
			RawResponse: &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader("")),
			},
		}
		got := formatAzureError(respErr)
		want := "403 Forbidden"
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("plain error falls back to err.Error()", func(t *testing.T) {
		err := errors.New("upsert failed: foreign key constraint")
		got := formatAzureError(err)
		want := "upsert failed: foreign key constraint"
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	})

	t.Run("nil returns empty", func(t *testing.T) {
		if got := formatAzureError(nil); got != "" {
			t.Errorf("got %q want empty", got)
		}
	})
}

// respErr builds an *azcore.ResponseError carrying the given status + ARM code.
func respErr(status int, code string) *azcore.ResponseError {
	return &azcore.ResponseError{
		ErrorCode:   code,
		StatusCode:  status,
		RawResponse: &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))},
	}
}

// respErrWithBody builds an *azcore.ResponseError with an ARM error body so the
// cached respErr.Error() string carries the message — mirrors RPs that report
// "subscription not registered" as a bare 400 with no error code.
func respErrWithBody(status int, code, message string) *azcore.ResponseError {
	body := fmt.Sprintf(`{"error":{"code":%q,"message":%q}}`, code, message)
	return &azcore.ResponseError{
		ErrorCode:   code,
		StatusCode:  status,
		RawResponse: &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))},
	}
}

// TestScanErrorClassifiers covers isSubscriptionNotRegistered and the unified
// isSkippableScanError predicate that gates scanner skip-and-continue branches.
func TestScanErrorClassifiers(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		notRegd      bool // want isSubscriptionNotRegistered
		skippable    bool // want isSkippableScanError
		accessDenied bool // want isAccessDenied (kept precise)
	}{
		{"404 SubscriptionNotRegistered", respErr(http.StatusNotFound, "SubscriptionNotRegistered"), true, true, false},
		{"409 MissingSubscriptionRegistration", respErr(http.StatusConflict, "MissingSubscriptionRegistration"), true, true, false},
		{"404 InvalidResourceType (api-version ahead of rollout)", respErr(http.StatusNotFound, "InvalidResourceType"), false, true, false},
		{"400 InvalidApiVersionParameter", respErr(http.StatusBadRequest, "InvalidApiVersionParameter"), false, true, false},
		{"403 AuthorizationFailed", respErr(http.StatusForbidden, "AuthorizationFailed"), false, true, true},
		{"401 unauthorized", respErr(http.StatusUnauthorized, "Unauthorized"), false, true, true},
		{"404 ResourceGroupNotFound (coded → not skippable)", respErr(http.StatusNotFound, "ResourceGroupNotFound"), false, false, false},
		{"404 bare/empty-code (operation not supported)", respErr(http.StatusNotFound, ""), false, true, false},
		{"400 empty-code 'Subscription not registered' (Microsoft.Maintenance)", respErrWithBody(http.StatusBadRequest, "", "Subscription not registered"), true, true, false},
		{"500 InternalError", respErr(http.StatusInternalServerError, "InternalError"), false, false, false},
		{"json syntax error (BOM-prefixed body)", &json.SyntaxError{}, false, true, false},
		{"json type mismatch (string into int32)", &json.UnmarshalTypeError{Value: "string", Type: reflect.TypeOf(int32(0))}, false, true, false},
		{"wrapped json type mismatch (azcore %w chain)", fmt.Errorf("unmarshalling type *armnetwork.X: %w", &json.UnmarshalTypeError{Value: "string", Type: reflect.TypeOf(int32(0))}), false, true, false},
		{"azcore %s-formatted unmarshal error (armpeering BOM, errors.As can't reach)", fmt.Errorf("armpeering:PeerAsns.ListBySubscription: %s", "unmarshalling type *armpeering.PeerAsnListResult: invalid character 'ï' looking for beginning of value"), false, true, false},
		{"plain error", errors.New("boom"), false, false, false},
		{"nil", nil, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSubscriptionNotRegistered(tc.err); got != tc.notRegd {
				t.Errorf("isSubscriptionNotRegistered(%v) = %v; want %v", tc.err, got, tc.notRegd)
			}
			if got := isSkippableScanError(tc.err); got != tc.skippable {
				t.Errorf("isSkippableScanError(%v) = %v; want %v", tc.err, got, tc.skippable)
			}
			if got := isAccessDenied(tc.err); got != tc.accessDenied {
				t.Errorf("isAccessDenied(%v) = %v; want %v", tc.err, got, tc.accessDenied)
			}
		})
	}
}

// TestSkipIfAccessDenied_Classification pins the split between "service not
// available in this subscription" (→ errServiceNotRegistered sentinel, no
// warning, dispatch marks the service disabled) and every other skippable
// condition (→ ScanWarning, nil error). The InvalidResourceType cases hinge on
// whether the message lists "supported api-versions": absent ⇒ RP/type not
// present ⇒ disabled; present ⇒ RP registered, SDK ahead of rollout ⇒ warning.
func TestSkipIfAccessDenied_Classification(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantSentinel bool // returns errServiceNotRegistered
		wantWarn     bool // emitted a ScanWarning
	}{
		{"SubscriptionNotRegistered", respErr(http.StatusNotFound, "SubscriptionNotRegistered"), true, false},
		{"MissingSubscriptionRegistration", respErr(http.StatusConflict, "MissingSubscriptionRegistration"), true, false},
		{"400 empty-code 'Subscription not registered'", respErrWithBody(http.StatusBadRequest, "", "Subscription not registered"), true, false},
		{"InvalidResourceType, RP/type absent (no supported-versions) → disabled", respErrWithBody(http.StatusNotFound, "InvalidResourceType", "The resource type could not be found in the namespace 'Microsoft.Orbital' for api version '2022-03-01'."), true, false},
		{"InvalidResourceType, api-version skew (supported-versions listed) → warns", respErrWithBody(http.StatusNotFound, "InvalidResourceType", "The resource type 'watchers' could not be found in the namespace 'Microsoft.DatabaseWatcher' for api version '2025-01-02'. The supported api-versions are '2023-09-01-preview'."), false, true},
		{"AccessDenied still warns", respErr(http.StatusForbidden, "AuthorizationFailed"), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var warnings int
			st := &store.Store{OnWarn: func(store.ScanWarning) { warnings++ }}
			got := skipIfAccessDenied(st, "azure:microsoft.test", "sub-123", tc.err)

			if isSentinel := errors.Is(got, errServiceNotRegistered); isSentinel != tc.wantSentinel {
				t.Errorf("skipIfAccessDenied err=%v; errServiceNotRegistered=%v, want %v", got, isSentinel, tc.wantSentinel)
			}
			if !tc.wantSentinel && got != nil {
				t.Errorf("skipIfAccessDenied returned %v; want nil for warnable error", got)
			}
			wantWarnings := 0
			if tc.wantWarn {
				wantWarnings = 1
			}
			if warnings != wantWarnings {
				t.Errorf("emitted %d warnings; want %d", warnings, wantWarnings)
			}
		})
	}
}
