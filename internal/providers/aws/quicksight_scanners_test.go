package aws

import (
	"net/http"
	"testing"

	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// TestQSSoftSkip covers the ListAgents region gap: newer QuickSight (Q) ops
// return a 404 HTML body the SDK can't map to a typed code, so qsSoftSkip falls
// back to the HTTP status. Access-denied and the existing typed codes still
// soft-skip; an unrelated error does not.
func TestQSSoftSkip(t *testing.T) {
	resp404 := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
		Err:      apiErr("UnknownError", "deserialization failed"),
	}
	if !qsSoftSkip(resp404) {
		t.Error("404 HTML response (ListAgents region gap) should soft-skip")
	}
	if !qsSoftSkip(apiErr("AccessDeniedException", "denied")) {
		t.Error("access-denied should soft-skip")
	}
	if !qsSoftSkip(apiErr("UnsupportedUserEditionException", "")) {
		t.Error("existing typed code should soft-skip")
	}
	if qsSoftSkip(apiErr("ValidationException", "bad input")) {
		t.Error("unrelated error must not soft-skip")
	}
}
