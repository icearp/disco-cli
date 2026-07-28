package aws

import (
	"bytes"
	"context"
	"io"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3control"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// maxBareErrorBodyBytes caps how much of an S3 Control error body
// rewrapBareXMLError will buffer. AWS error bodies are a few hundred bytes; the
// cap exists so a malformed or hostile response cannot make the scanner hold an
// unbounded buffer per in-flight request. A body larger than this is passed
// through untouched rather than truncated — a truncated XML document would
// deserialize worse than the original.
const maxBareErrorBodyBytes = 64 << 10

// errorResponseOpen and errorResponseClose are the envelope the generated S3
// Control deserializers expect around an <Error> element.
const (
	errorResponseOpen  = "<ErrorResponse>"
	errorResponseClose = "</ErrorResponse>"
)

// newS3ControlClient builds an S3 Control client for a region with the bare
// <Error> repair installed. Every S3 Control client in this package goes through
// here: the malformed envelope is a property of the API, not of one scanner, so
// a client constructed without the repair silently reports "UnknownError"
// instead of whatever AWS actually said.
func newS3ControlClient(cfg sdkaws.Config, region string) *s3control.Client {
	return s3control.NewFromConfig(cfg, func(o *s3control.Options) {
		o.Region = region
		o.APIOptions = append(o.APIOptions, normalizeBareXMLError)
	})
}

// normalizeBareXMLError registers rewrapBareXMLError on an S3 Control client's
// middleware stack, via Options.APIOptions. Prefer newS3ControlClient over
// calling this directly.
//
// It inserts AFTER the operation deserializer, which places it CLOSER to the
// transport: a smithy step runs its list head-outermost and wires the transport
// onto the tail (middleware/step_deserialize.go, `s.tail.Next = wh`), so the
// response reaches this middleware before the deserializer that would consume
// it. Insert reports an error if "OperationDeserializer" is ever renamed, so an
// upstream change surfaces at client construction rather than silently
// disabling the fix.
func normalizeBareXMLError(stack *middleware.Stack) error {
	return stack.Deserialize.Insert(rewrapBareXMLError{}, "OperationDeserializer", middleware.After)
}

// rewrapBareXMLError repairs S3 Control error bodies whose root element is
// <Error>, so the generated deserializers can read the code and message out of
// them.
//
// The generated error deserializers parse with
// s3shared.ErrorResponseDeserializerOptions{IsWrappedWithErrorTag: true}, whose
// struct binds `xml:"Error>Code"` and `xml:"Error>Message"` — it needs an
// <Error> CHILD of the root. Most of the API obliges:
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<ErrorResponse><Error><Code>AccessDenied</Code>…</Error>…</ErrorResponse>
//
// S3 Storage Lens does not. Its region rejections send <Error> AS the root, so
// Code and Message bind to nothing and the SDK reports the placeholder
// "UnknownError: UnknownError", while RequestId and HostId — direct children of
// that root — still bind. The real text ("Region is not supported as home
// region for S3 Storage Lens") never reaches the caller, and a message-based
// predicate cannot tell that availability gap from a genuine IAM denial.
//
// Wrapping such a body in <ErrorResponse> restores the shape the parser expects.
// A body that is already wrapped, a success response, and a transport failure
// are all passed through untouched.
type rewrapBareXMLError struct{}

// ID implements middleware.DeserializeMiddleware.
func (rewrapBareXMLError) ID() string { return "DiscoRewrapBareXMLError" }

// HandleDeserialize implements middleware.DeserializeMiddleware.
func (rewrapBareXMLError) HandleDeserialize(
	ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler,
) (middleware.DeserializeOutput, middleware.Metadata, error) {
	out, metadata, err := next.HandleDeserialize(ctx, in)
	if err != nil {
		return out, metadata, err
	}
	resp, ok := out.RawResponse.(*smithyhttp.Response)
	if !ok || resp == nil || resp.Body == nil || resp.StatusCode < 400 {
		return out, metadata, err
	}

	// Read one byte past the cap so an oversized body is detectable without
	// buffering all of it.
	head, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBareErrorBodyBytes+1))
	if readErr != nil || len(head) > maxBareErrorBodyBytes {
		// Hand the deserializer the original stream back, whole: what was already
		// read, followed by whatever remains. Truncating here would turn a body
		// this middleware merely declined to repair into invalid XML — a worse
		// outcome than the unreadable-but-intact body it started with.
		resp.Body = rejoinedBody{Reader: io.MultiReader(bytes.NewReader(head), resp.Body), Closer: resp.Body}
		return out, metadata, err
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(wrapBareXMLError(head)))
	return out, metadata, err
}

// rejoinedBody rejoins a partially-consumed body to its original Closer, so
// replacing the stream does not leak the underlying connection.
type rejoinedBody struct {
	io.Reader
	io.Closer
}

// wrapBareXMLError returns body wrapped in an <ErrorResponse> envelope when its
// root element is <Error>, and body unchanged otherwise. Any XML declaration or
// leading whitespace stays ahead of the inserted open tag, because a declaration
// is only legal as the very first thing in the document.
func wrapBareXMLError(body []byte) []byte {
	rootAt, ok := xmlRootOffset(body)
	if !ok || !bytes.HasPrefix(body[rootAt:], []byte("<Error")) {
		return body
	}
	// Guard against a root named e.g. <ErrorResponse>: only "<Error>" and
	// "<Error " (an element carrying attributes) are the bare form.
	rest := body[rootAt+len("<Error"):]
	if len(rest) == 0 {
		return body
	}
	switch rest[0] {
	case '>', ' ', '\t', '\r', '\n', '/':
	default:
		return body
	}

	wrapped := make([]byte, 0, len(body)+len(errorResponseOpen)+len(errorResponseClose))
	wrapped = append(wrapped, body[:rootAt]...)
	wrapped = append(wrapped, errorResponseOpen...)
	wrapped = append(wrapped, body[rootAt:]...)
	wrapped = append(wrapped, errorResponseClose...)
	return wrapped
}

// xmlRootOffset returns the index of the document's root element, skipping any
// leading whitespace, XML declaration (<?…?>), comment (<!--…-->) or doctype
// (<!…>). Reports false when no element start is found.
func xmlRootOffset(body []byte) (int, bool) {
	for i := 0; i < len(body); {
		switch body[i] {
		case ' ', '\t', '\r', '\n':
			i++
			continue
		case '<':
			if i+1 >= len(body) {
				return 0, false
			}
			if body[i+1] != '?' && body[i+1] != '!' {
				return i, true
			}
			// A comment may contain '>' freely, so it ends at "-->", not at the
			// next '>'. Everything else in the prolog ends at the next '>'.
			closer := []byte(">")
			if bytes.HasPrefix(body[i:], []byte("<!--")) {
				closer = []byte("-->")
			}
			end := bytes.Index(body[i:], closer)
			if end < 0 {
				return 0, false
			}
			i += end + len(closer)
		default:
			return 0, false
		}
	}
	return 0, false
}
