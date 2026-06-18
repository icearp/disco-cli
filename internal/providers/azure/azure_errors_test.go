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
		{"500 InternalError", respErr(http.StatusInternalServerError, "InternalError"), false, false, false},
		{"json syntax error (BOM-prefixed body)", &json.SyntaxError{}, false, true, false},
		{"json type mismatch (string into int32)", &json.UnmarshalTypeError{Value: "string", Type: reflect.TypeOf(int32(0))}, false, true, false},
		{"wrapped json type mismatch (azcore %w chain)", fmt.Errorf("unmarshalling type *armnetwork.X: %w", &json.UnmarshalTypeError{Value: "string", Type: reflect.TypeOf(int32(0))}), false, true, false},
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
