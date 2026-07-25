package aws

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/icearp/disco-cli/store"
)

// stubSQS is an in-memory sqsAPI for unit tests. It pages QueueUrls in
// fixed-size chunks sized by listPageSize and serves per-queue attribute
// responses from attrs. listErr / attrErr (keyed by URL) inject failures
// into specific calls.
type stubSQS struct {
	urls         []string
	listPageSize int
	attrs        map[string]map[string]string
	listErr      error            // returned from ListQueues on first page
	attrErr      map[string]error // keyed by QueueUrl
}

func (s *stubSQS) ListQueues(_ context.Context, in *sqs.ListQueuesInput, _ ...func(*sqs.Options)) (*sqs.ListQueuesOutput, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	start := 0
	if in.NextToken != nil {
		// Token format: byte-offset string; tests only need monotonic ordering.
		// Empty token = start, else interpreted via len() of the string —
		// see test fixtures.
		start = len(*in.NextToken)
	}
	end := start + s.listPageSize
	if s.listPageSize == 0 || end > len(s.urls) {
		end = len(s.urls)
	}
	out := &sqs.ListQueuesOutput{QueueUrls: s.urls[start:end]}
	if end < len(s.urls) {
		// Token whose len equals next start offset.
		tok := make([]byte, end)
		t := string(tok)
		out.NextToken = &t
	}
	return out, nil
}

func (s *stubSQS) GetQueueAttributes(_ context.Context, in *sqs.GetQueueAttributesInput, _ ...func(*sqs.Options)) (*sqs.GetQueueAttributesOutput, error) {
	url := ""
	if in.QueueUrl != nil {
		url = *in.QueueUrl
	}
	if e, ok := s.attrErr[url]; ok {
		return nil, e
	}
	a := s.attrs[url]
	if a == nil {
		a = map[string]string{}
	}
	// Return a copy; production code mutates the map (adds QueueUrl).
	cp := make(map[string]string, len(a))
	for k, v := range a {
		cp[k] = v
	}
	return &sqs.GetQueueAttributesOutput{Attributes: cp}, nil
}

func TestScanSQSQueues_PersistsQueuesAndArn(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	url1 := "https://sqs.us-east-1.amazonaws.com/123456789012/main"
	url2 := "https://sqs.us-east-1.amazonaws.com/123456789012/dlq"
	arn1 := "arn:aws:sqs:us-east-1:123456789012:main"
	arn2 := "arn:aws:sqs:us-east-1:123456789012:dlq"
	keyARN := "arn:aws:kms:us-east-1:123456789012:key/abcd"

	stub := &stubSQS{
		urls:         []string{url1, url2},
		listPageSize: 1, // force two pages → exercises paginator state
		attrs: map[string]map[string]string{
			url1: {"QueueArn": arn1, "KmsMasterKeyId": keyARN},
			url2: {"QueueArn": arn2},
		},
	}

	total, inserted, err := scanSQSQueues(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanSQSQueues: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total=%d inserted=%d, want 2/2", total, inserted)
	}

	// Verify resources landed and AttributesJSON includes the synthesized
	// QueueUrl entry the scanner adds alongside SDK attrs.
	got, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, Types: []string{TypeSQSQueue}, Limit: 100,
	})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d queue rows, want 2", len(got))
	}
	for _, r := range got {
		var m map[string]string
		if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
			t.Errorf("attrs not JSON: %v", err)
		}
		if m["QueueUrl"] == "" {
			t.Errorf("AttributesJSON missing QueueUrl synthetic field for %s", r.NativeID)
		}
	}
}

func TestScanSQSQueues_AccessDeniedOnListReturnsNilWarning(t *testing.T) {
	st := newTestStore(t)
	var got store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { got = w }
	acct := newTestAccount(testAccountID)

	stub := &stubSQS{listErr: apiErr("AccessDenied", "denied")}

	total, inserted, err := scanSQSQueues(context.Background(), stub, acct, "us-east-1", st, testScanID)
	if err != nil {
		t.Fatalf("scanSQSQueues returned err: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0", total, inserted)
	}
	if got.Service != "sqs:ListQueues" {
		t.Errorf("warning service = %q, want sqs:ListQueues", got.Service)
	}
}

func TestScanSQSQueues_ListErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubSQS{listErr: errors.New("network kaput")}

	_, _, err := scanSQSQueues(context.Background(), stub, acct, "us-east-1", st, testScanID)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestScanSQSQueues_PerQueueAccessDeniedSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	url1 := "https://sqs.us-east-1.amazonaws.com/123456789012/q1"
	url2 := "https://sqs.us-east-1.amazonaws.com/123456789012/q2"

	stub := &stubSQS{
		urls:         []string{url1, url2},
		listPageSize: 10,
		attrs: map[string]map[string]string{
			url1: {"QueueArn": "arn:aws:sqs:us-east-1:123456789012:q1"},
		},
		attrErr: map[string]error{
			url2: apiErr("AccessDenied", "no read on q2"),
		},
	}

	total, inserted, err := scanSQSQueues(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanSQSQueues: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1 (q2 skipped)", total, inserted)
	}
}

func TestScanSQSQueues_MissingQueueArnSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	url := "https://sqs.us-east-1.amazonaws.com/123456789012/orphan"
	stub := &stubSQS{
		urls:         []string{url},
		listPageSize: 10,
		attrs:        map[string]map[string]string{url: {}}, // no QueueArn
	}

	total, inserted, err := scanSQSQueues(context.Background(), stub, acct, "us-east-1", st, testScanID)
	if err != nil {
		t.Fatalf("scanSQSQueues: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0 (queue without QueueArn skipped)", total, inserted)
	}
}
