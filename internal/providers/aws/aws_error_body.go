package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// maxErrorBodyBytes caps how much of an error body rewrapBareXMLError and
// errorMessageKeyRepair will buffer. AWS error bodies are a few hundred bytes;
// the cap exists so a malformed or hostile response cannot make the scanner hold
// an unbounded buffer per in-flight request. A body larger than this is passed
// through untouched rather than truncated — a truncated document would
// deserialize worse than the original.
const maxErrorBodyBytes = 64 << 10

// passBodyThrough hands the deserializer the response body back as it would
// have read it without the middleware: the bytes already read, then the rest of
// the stream, or, when the read failed, the same read error. Re-reading a
// stream that has already failed is not guaranteed to fail again, and a clean
// EOF there would turn a transport failure into a truncated body.
func passBodyThrough(resp *smithyhttp.Response, head []byte, readErr error) {
	var rest io.Reader = resp.Body
	if readErr != nil {
		rest = errReader{readErr}
	}
	resp.Body = rejoinedBody{Reader: io.MultiReader(bytes.NewReader(head), rest), Closer: resp.Body}
}

// rejoinedBody rejoins a partially-consumed body to its original Closer, so
// replacing the stream does not leak the underlying connection.
type rejoinedBody struct {
	io.Reader
	io.Closer
}

// errReader fails every Read with err.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

// normalizeErrorMessageKey registers errorMessageKeyRepair on a client's
// middleware stack. loadAccounts installs it on the base config, so every
// client built from an account config carries it.
//
// It inserts AFTER the operation deserializer, which places it closer to the
// transport, for the reason given on normalizeBareXMLError.
func normalizeErrorMessageKey(stack *middleware.Stack) error {
	return stack.Deserialize.Insert(errorMessageKeyRepair{}, "OperationDeserializer", middleware.After)
}

// errorMessageKeyRepair gives a JSON error body both spellings of its message
// key, "message" and "Message", so the SDK reads the message whichever one the
// model names.
//
// Service modules generated with schema-based (de)serialization (CHANGELOG
// "Enable schema-based (de)serialization") decode a modeled error with the
// smithy-go JSON shape deserializer, which matches member names exactly. The
// gateway in front of a service can spell the key differently from the model:
// SES's gateway answers "Unable to determine service/operation name to be
// authorized" under "Message", and many models name the member "message". The
// typed error
// then carries no message at all: nothing else in the error chain keeps it, as
// the protocol's own case-insensitive read is used only for errors the model
// does not declare. Every message-based predicate in aws_errors.go would read
// "", and isClosedToNewCustomers would mistake a real IAM denial for a closed
// service. Older modules match the key case-insensitively, so for them the
// added key repeats a value they already read.
//
// Only a JSON object holding exactly one of the two keys is changed. XML and
// CBOR bodies are passed through: the XML shape deserializer matches element
// names case-insensitively, and CBOR bodies are written by the service from its
// own model.
type errorMessageKeyRepair struct{}

// ID implements middleware.DeserializeMiddleware.
func (errorMessageKeyRepair) ID() string { return "DiscoErrorMessageKeyRepair" }

// HandleDeserialize implements middleware.DeserializeMiddleware.
func (errorMessageKeyRepair) HandleDeserialize(
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

	head, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
	if readErr != nil || len(head) > maxErrorBodyBytes {
		passBodyThrough(resp, head, readErr)
		return out, metadata, err
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(withBothMessageKeys(head)))
	return out, metadata, err
}

// withBothMessageKeys returns body with the missing one of "message" and
// "Message" added, carrying the other's raw value, when body is a JSON object
// holding exactly one of them. Any other body is returned unchanged.
//
// The key is spliced in after the opening brace rather than re-marshalling the
// object, so every other byte of the body (key order, number formatting,
// duplicate keys) reaches the deserializer as AWS sent it.
func withBothMessageKeys(body []byte) []byte {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return body
	}
	lower, hasLower := fields["message"]
	upper, hasUpper := fields["Message"]
	var key string
	var value json.RawMessage
	switch {
	case hasUpper && !hasLower:
		key, value = "message", upper
	case hasLower && !hasUpper:
		key, value = "Message", lower
	default:
		return body
	}

	// The object holds at least the key just found, so the inserted member is
	// always followed by another and needs the trailing comma.
	open := bytes.IndexByte(body, '{')
	out := make([]byte, 0, len(body)+len(key)+len(value)+4)
	out = append(out, body[:open+1]...)
	out = append(out, '"')
	out = append(out, key...)
	out = append(out, `":`...)
	out = append(out, value...)
	out = append(out, ',')
	return append(out, body[open+1:]...)
}
