package aws

import (
	"encoding/json"
	"testing"

	logsTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"codeberg.org/icearp/disco/internal/volatile"
)

func ptrI64(v int64) *int64 { return &v }

func logStreamJSON(t *testing.T, s logsTypes.LogStream) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal LogStream: %v", err)
	}
	return string(b)
}

// TestVolatile_LogStream_DropsUploadSequenceToken pins the registered rule
// against the real SDK shape — an UploadSequenceToken field rename breaks this
// on `go mod tidy`, the same drift catch aws_redact_test.go provides.
func TestVolatile_LogStream_DropsUploadSequenceToken(t *testing.T) {
	s := logsTypes.LogStream{
		LogStreamName:       ptrStr("2026/06/26/[$LATEST]abc"),
		Arn:                 ptrStr("arn:aws:logs:us-east-2:111:log-group:/g:log-stream:s"),
		LastEventTimestamp:  ptrI64(1782446744685),
		UploadSequenceToken: ptrStr("49039859609806851380763974087743987527351036997711328602"),
	}
	out := volatile.Apply(TypeLogsLogStream, logStreamJSON(t, s))

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if _, ok := got["UploadSequenceToken"]; ok {
		t.Errorf("UploadSequenceToken should be dropped, got %v", got["UploadSequenceToken"])
	}
	// Real fields survive.
	if got["LogStreamName"] != "2026/06/26/[$LATEST]abc" {
		t.Errorf("LogStreamName altered: %v", got["LogStreamName"])
	}
	if _, ok := got["LastEventTimestamp"]; !ok {
		t.Error("LastEventTimestamp dropped — only UploadSequenceToken should be removed")
	}
}

// TestVolatile_LogStream_NoSplitOnTokenChange is the behavioral guard: two
// scans of a stream that differ ONLY in the rotating UploadSequenceToken must
// not version-split (the aws init() registers the rule; UpsertResources runs
// volatile.Apply before its jsonEqual comparison).
func TestVolatile_LogStream_NoSplitOnTokenChange(t *testing.T) {
	st := newTestStore(t)

	base := logsTypes.LogStream{
		LogStreamName:       ptrStr("s1"),
		LastEventTimestamp:  ptrI64(100),
		UploadSequenceToken: ptrStr("token-A"),
	}
	rootID := upsertTestResource(t, st, "aws", "acct", TypeLogsLogStream,
		"/g/stream/s1", "us-east-2", logStreamJSON(t, base))

	// Second scan: identical except the rotated token.
	base.UploadSequenceToken = ptrStr("token-B")
	upsertTestResource(t, st, "aws", "acct", TypeLogsLogStream,
		"/g/stream/s1", "us-east-2", logStreamJSON(t, base))

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("token-only change must NOT split: got %d versions", len(versions))
	}
}

// TestVolatile_LogStream_SplitOnRealChange is the negative-space companion: a
// real activity change (LastEventTimestamp advancing) still version-splits.
func TestVolatile_LogStream_SplitOnRealChange(t *testing.T) {
	st := newTestStore(t)

	base := logsTypes.LogStream{
		LogStreamName:       ptrStr("s1"),
		LastEventTimestamp:  ptrI64(100),
		UploadSequenceToken: ptrStr("token-A"),
	}
	rootID := upsertTestResource(t, st, "aws", "acct", TypeLogsLogStream,
		"/g/stream/s1", "us-east-2", logStreamJSON(t, base))

	base.LastEventTimestamp = ptrI64(200) // new event ingested
	base.UploadSequenceToken = ptrStr("token-B")
	upsertTestResource(t, st, "aws", "acct", TypeLogsLogStream,
		"/g/stream/s1", "us-east-2", logStreamJSON(t, base))

	versions, err := st.GetResourceVersions(rootID)
	if err != nil {
		t.Fatalf("GetResourceVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("real activity change must split: got %d versions", len(versions))
	}
}
