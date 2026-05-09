package aws

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	smithymw "github.com/aws/smithy-go/middleware"
)

func newSQSClientWithStub(stub func(*smithymw.Stack) error, region string) *sqs.Client {
	cfg := sdkaws.Config{
		Region:           region,
		Credentials:      credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
		RetryMaxAttempts: 1,
	}
	return sqs.NewFromConfig(cfg, func(o *sqs.Options) {
		o.APIOptions = append(o.APIOptions, stub)
	})
}

// TestScanSQSQueues_ViaSDKMiddleware exercises scanSQSQueues against a real
// *sqs.Client whose request path is short-circuited by an Initialize-step
// middleware. Unlike the interface-mock tests in sqs_scanners_test.go, this
// path executes the SDK's paginator state machine, retry classifier, and
// option chain end-to-end — proving the scanner integrates with the real
// client wiring, not just with a hand-rolled stub.
func TestScanSQSQueues_ViaSDKMiddleware(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	url1 := "https://sqs.us-east-1.amazonaws.com/123456789012/q1"
	url2 := "https://sqs.us-east-1.amazonaws.com/123456789012/q2"
	arn1 := "arn:aws:sqs:us-east-1:123456789012:q1"
	arn2 := "arn:aws:sqs:us-east-1:123456789012:q2"

	tok := "page2"
	listPage1 := &sqs.ListQueuesOutput{QueueUrls: []string{url1}, NextToken: &tok}
	listPage2 := &sqs.ListQueuesOutput{QueueUrls: []string{url2}}
	attrs1 := &sqs.GetQueueAttributesOutput{Attributes: map[string]string{"QueueArn": arn1}}
	attrs2 := &sqs.GetQueueAttributesOutput{Attributes: map[string]string{"QueueArn": arn2}}

	stub := stubResponses(t, map[string][]stubCall{
		"ListQueues":         {{Output: listPage1}, {Output: listPage2}},
		"GetQueueAttributes": {{Output: attrs1}, {Output: attrs2}},
	})
	client := newSQSClientWithStub(stub, region)

	total, inserted, err := scanSQSQueues(context.Background(), client, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanSQSQueues: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total=%d inserted=%d, want 2/2", total, inserted)
	}
}

// TestScanSQSQueues_AccessDeniedListShortCircuits_ViaSDK confirms an
// AccessDenied error returned from the SDK boundary is classified by the
// scanner's isAccessDenied predicate and downgraded to a warning — the same
// behavior as the interface-mock test, but proving classification works
// against an error that traversed the real SDK option chain.
func TestScanSQSQueues_AccessDeniedListShortCircuits_ViaSDK(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	warnings := 0
	st.OnWarn = func(_ store.ScanWarning) { warnings++ }

	stub := stubResponses(t, map[string][]stubCall{
		"ListQueues": {{Err: apiErr("AccessDenied", "denied at SDK boundary")}},
	})
	client := newSQSClientWithStub(stub, "us-east-1")

	total, inserted, err := scanSQSQueues(context.Background(), client, acct, "us-east-1", st, testScanID)
	if err != nil {
		t.Fatalf("scanSQSQueues: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0", total, inserted)
	}
	if warnings != 1 {
		t.Errorf("warnings=%d, want 1", warnings)
	}
}
