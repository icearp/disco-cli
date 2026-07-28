package aws

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// bareStorageLensError is the response body AWS actually returns for
// ListStorageLensConfigurations in a region that is not a supported home
// region, captured off the wire in ap-northeast-3. <Error> is the ROOT element,
// which is what defeats the generated deserializer.
const bareStorageLensError = `<Error><Code>AccessDenied</Code>` +
	`<Message>Region is not supported as home region for S3 Storage Lens</Message>` +
	`<RequestId>MHNVVZ4E9YKWKKKG</RequestId><HostId>abc123</HostId></Error>`

// wrappedS3ControlError is the shape the rest of the API uses, also captured
// off the wire. It must survive rewrapping untouched.
const wrappedS3ControlError = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
	`<ErrorResponse><Error><Code>AccessDenied</Code><Message>Access Denied</Message>` +
	`<AccountId>111111111111</AccountId></Error><RequestId>27C4S25A5J7S0EHV</RequestId></ErrorResponse>`

// TestWrapBareXMLError is the core guard. The pass-through rows matter as much
// as the wrapping row: this middleware sees every S3 Control error body, so a
// rule that fired too eagerly would corrupt responses the SDK parses correctly
// today — turning a working error path into an unreadable one, which is the
// exact failure it exists to fix.
func TestWrapBareXMLError(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string // "" means: expect the input back byte-identical
	}{
		{
			name: "bare Error root is wrapped",
			body: bareStorageLensError,
			want: errorResponseOpen + bareStorageLensError + errorResponseClose,
		},
		{
			name: "already wrapped body is untouched",
			body: wrappedS3ControlError,
		},
		{
			// <ErrorResponse> starts with "<Error" — the prefix check alone
			// would wrap it and break every error the SDK reads today.
			name: "ErrorResponse root is not mistaken for a bare Error",
			body: `<ErrorResponse><Error><Code>AccessDenied</Code></Error></ErrorResponse>`,
		},
		{
			name: "declaration stays leading when wrapping",
			body: `<?xml version="1.0"?><Error><Code>AccessDenied</Code></Error>`,
			want: `<?xml version="1.0"?>` + errorResponseOpen +
				`<Error><Code>AccessDenied</Code></Error>` + errorResponseClose,
		},
		{
			name: "Error root with attributes is wrapped",
			body: `<Error xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Code>AccessDenied</Code></Error>`,
			want: errorResponseOpen +
				`<Error xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Code>AccessDenied</Code></Error>` +
				errorResponseClose,
		},
		{
			// A comment carries a '>' inside it; scanning to the next '>' would
			// mislocate the root and wrap at the wrong offset.
			name: "comment containing an angle bracket is skipped correctly",
			body: `<!-- a > b --><Error><Code>AccessDenied</Code></Error>`,
			want: `<!-- a > b -->` + errorResponseOpen +
				`<Error><Code>AccessDenied</Code></Error>` + errorResponseClose,
		},
		{name: "empty body is untouched", body: ""},
		{name: "non-XML body is untouched", body: "not xml at all"},
		{name: "truncated root is untouched", body: "<Error"},
		{name: "unterminated declaration is untouched", body: `<?xml version="1.0"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			if want == "" {
				want = tc.body
			}
			if got := string(wrapBareXMLError([]byte(tc.body))); got != want {
				t.Errorf("wrapBareXMLError(%q)\n got %q\nwant %q", tc.body, got, want)
			}
		})
	}
}

// TestWrapBareXMLError_OutputParsesAsWrapped proves the repair is not merely a
// string edit: the wrapped document must expose Code and Message where the
// generated deserializer looks for them (xml:"Error>Code"). Asserting on the
// bytes alone would pass even if the envelope were subtly wrong.
func TestWrapBareXMLError_OutputParsesAsWrapped(t *testing.T) {
	var before, after struct {
		Code    string `xml:"Error>Code"`
		Message string `xml:"Error>Message"`
	}

	if err := xml.Unmarshal([]byte(bareStorageLensError), &before); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if before.Code != "" || before.Message != "" {
		t.Fatalf("raw body already parsed (%q/%q) — the bug this repairs is gone, revisit the fix",
			before.Code, before.Message)
	}

	if err := xml.Unmarshal(wrapBareXMLError([]byte(bareStorageLensError)), &after); err != nil {
		t.Fatalf("unmarshal wrapped: %v", err)
	}
	if after.Code != "AccessDenied" {
		t.Errorf("Code = %q, want AccessDenied", after.Code)
	}
	if !strings.Contains(after.Message, "home region") {
		t.Errorf("Message = %q, want it to carry the home-region text", after.Message)
	}
}

// stubDeserializeHandler returns a canned response, standing in for the
// transport at the bottom of the middleware chain.
type stubDeserializeHandler struct {
	status int
	body   string
	err    error
	raw    any // when non-nil, returned instead of a *smithyhttp.Response
}

func (s stubDeserializeHandler) HandleDeserialize(context.Context, middleware.DeserializeInput) (
	middleware.DeserializeOutput, middleware.Metadata, error,
) {
	if s.err != nil {
		return middleware.DeserializeOutput{}, middleware.Metadata{}, s.err
	}
	out := middleware.DeserializeOutput{RawResponse: s.raw}
	if s.raw == nil {
		out.RawResponse = &smithyhttp.Response{Response: &http.Response{
			StatusCode: s.status,
			Body:       io.NopCloser(strings.NewReader(s.body)),
		}}
	}
	return out, middleware.Metadata{}, nil
}

// TestRewrapBareXMLError_HandleDeserialize covers the wiring around the repair:
// which responses it touches, and that it always leaves a readable body behind
// for the deserializer that runs next.
func TestRewrapBareXMLError_HandleDeserialize(t *testing.T) {
	readBody := func(t *testing.T, out middleware.DeserializeOutput) string {
		t.Helper()
		resp, ok := out.RawResponse.(*smithyhttp.Response)
		if !ok || resp.Body == nil {
			t.Fatalf("no response body on output %#v", out.RawResponse)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return string(b)
	}

	t.Run("error response is repaired", func(t *testing.T) {
		out, _, err := rewrapBareXMLError{}.HandleDeserialize(context.Background(),
			middleware.DeserializeInput{}, stubDeserializeHandler{status: 403, body: bareStorageLensError})
		if err != nil {
			t.Fatalf("HandleDeserialize: %v", err)
		}
		if got := readBody(t, out); !strings.HasPrefix(got, errorResponseOpen) {
			t.Errorf("body = %q, want it wrapped", got)
		}
	})

	t.Run("success response is left alone", func(t *testing.T) {
		// A 2xx body is the deserializer's real payload; touching it would be a
		// far worse bug than the one being fixed.
		const payload = `<Error>not an error, just a field name</Error>`
		out, _, err := rewrapBareXMLError{}.HandleDeserialize(context.Background(),
			middleware.DeserializeInput{}, stubDeserializeHandler{status: 200, body: payload})
		if err != nil {
			t.Fatalf("HandleDeserialize: %v", err)
		}
		if got := readBody(t, out); got != payload {
			t.Errorf("body = %q, want it untouched", got)
		}
	})

	t.Run("transport error passes through", func(t *testing.T) {
		sentinel := errors.New("dial tcp: connection refused")
		_, _, err := rewrapBareXMLError{}.HandleDeserialize(context.Background(),
			middleware.DeserializeInput{}, stubDeserializeHandler{err: sentinel})
		if !errors.Is(err, sentinel) {
			t.Errorf("err = %v, want the transport error unchanged", err)
		}
	})

	t.Run("oversized body is passed through whole, not truncated", func(t *testing.T) {
		// Declining to repair must not damage the body. Handing the deserializer
		// the first N bytes of an XML document is strictly worse than handing it
		// the unparseable-but-intact original: truncation turns a readable error
		// into a parse failure.
		big := "<Error>" + strings.Repeat("x", maxBareErrorBodyBytes) + "</Error>"
		out, _, err := rewrapBareXMLError{}.HandleDeserialize(context.Background(),
			middleware.DeserializeInput{}, stubDeserializeHandler{status: 403, body: big})
		if err != nil {
			t.Fatalf("HandleDeserialize: %v", err)
		}
		got := readBody(t, out)
		if strings.HasPrefix(got, errorResponseOpen) {
			t.Error("oversized body was wrapped; want it passed through")
		}
		if got != big {
			t.Errorf("oversized body was altered: got %d bytes, want the original %d", len(got), len(big))
		}
	})

	t.Run("non-HTTP raw response is ignored", func(t *testing.T) {
		out, _, err := rewrapBareXMLError{}.HandleDeserialize(context.Background(),
			middleware.DeserializeInput{}, stubDeserializeHandler{raw: struct{}{}})
		if err != nil {
			t.Fatalf("HandleDeserialize: %v", err)
		}
		if _, ok := out.RawResponse.(*smithyhttp.Response); ok {
			t.Error("raw response was replaced; want it left as-is")
		}
	})
}

// TestNormalizeBareXMLError_Registers pins the relative insert. If
// "OperationDeserializer" is ever renamed upstream, Insert errors here rather
// than silently leaving the middleware out of the chain and letting the
// UnknownError regression return unnoticed.
func TestNormalizeBareXMLError_Registers(t *testing.T) {
	stack := middleware.NewStack("test", func() any { return nil })
	if err := stack.Deserialize.Add(namedDeserializeNoop("OperationDeserializer"), middleware.After); err != nil {
		t.Fatalf("seed OperationDeserializer: %v", err)
	}
	if err := normalizeBareXMLError(stack); err != nil {
		t.Fatalf("normalizeBareXMLError: %v", err)
	}

	ids := stack.Deserialize.List()
	opAt, mwAt := indexOf(ids, "OperationDeserializer"), indexOf(ids, rewrapBareXMLError{}.ID())
	if mwAt < 0 {
		t.Fatalf("middleware not registered; stack = %v", ids)
	}
	// Later in the list is closer to the transport, which is the only position
	// where the raw body is still unread.
	if mwAt < opAt {
		t.Errorf("middleware at %d is outside OperationDeserializer at %d; it would see a drained body", mwAt, opAt)
	}
}

func indexOf(ids []string, want string) int {
	for i, id := range ids {
		if id == want {
			return i
		}
	}
	return -1
}

// namedDeserializeNoop is a placeholder middleware used only to give the stack a
// relative-insert anchor.
func namedDeserializeNoop(id string) middleware.DeserializeMiddleware {
	return middleware.DeserializeMiddlewareFunc(id,
		func(ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler) (
			middleware.DeserializeOutput, middleware.Metadata, error,
		) {
			return next.HandleDeserialize(ctx, in)
		})
}
