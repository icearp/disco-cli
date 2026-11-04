package aws

import (
	"context"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/licensemanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	smithymw "github.com/aws/smithy-go/middleware"
	"github.com/icearp/disco-cli/store"
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

// TestScanSQSQueues_ViaSDKMiddleware runs scanSQSQueues against a real
// *sqs.Client whose requests are short-circuited by an Initialize-step
// middleware. Unlike the interface-mock tests in sqs_scanners_test.go, this
// exercises the SDK's paginator, retry classifier, and option chain
// end-to-end — proving the scanner works against real client wiring, not
// just a hand-rolled stub.
func TestScanSQSQueues_ViaSDKMiddleware(t *testing.T) {
	t.Parallel()
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
// AccessDenied error from the SDK boundary is classified by isAccessDenied
// and downgraded to a warning — same behavior as the interface-mock test,
// but proving classification works on an error that traversed the real SDK
// option chain.
func TestScanSQSQueues_AccessDeniedListShortCircuits_ViaSDK(t *testing.T) {
	t.Parallel()
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

// TestStubHarnessesReturnFreshOutput proves each stub harness answers a call
// with a copy of its fixture, never the fixture itself. The SDK writes
// ResultMetadata into the output a call returns, so a shared fixture would be
// written by every concurrent call; plain go test cannot see that write, only
// -race can.
func TestStubHarnessesReturnFreshOutput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queues := &sqs.ListQueuesOutput{}
	buckets := &s3.ListBucketsOutput{}
	licenses := &licensemanager.ListLicensesOutput{}
	cases := []struct {
		harness string
		fixture any
		call    func() (any, error)
	}{
		{"stubResponses", queues, func() (any, error) {
			stub := stubResponses(t, map[string][]stubCall{"ListQueues": {{Output: queues}}})
			return sqs.NewFromConfig(cfgWithStub(stub, testRegion)).ListQueues(ctx, &sqs.ListQueuesInput{})
		}},
		{"opStubConfig", queues, func() (any, error) {
			cfg := opStubConfig(map[string]func(any) (any, error){
				"ListQueues": func(any) (any, error) { return queues, nil },
			}, nil)
			return sqs.NewFromConfig(cfg).ListQueues(ctx, &sqs.ListQueuesInput{})
		}},
		{"s3StubConfig", buckets, func() (any, error) {
			cfg := s3StubConfig(map[string]func(string, any) (any, error){
				"ListBuckets": func(string, any) (any, error) { return buckets, nil },
			})
			return s3.NewFromConfig(cfg).ListBuckets(ctx, &s3.ListBucketsInput{})
		}},
		{"lmRetryingConfig", licenses, func() (any, error) {
			cfg := lmRetryingConfig(map[string]func(any) (any, error){
				"ListLicenses": func(any) (any, error) { return licenses, nil },
			})
			return licensemanager.NewFromConfig(cfg).ListLicenses(ctx, &licensemanager.ListLicensesInput{})
		}},
	}
	for _, tc := range cases {
		got, err := tc.call()
		if err != nil {
			t.Fatalf("%s: call failed: %v", tc.harness, err)
		}
		if got == tc.fixture {
			t.Errorf("%s returned its fixture itself; wrap the output in freshOutput", tc.harness)
		}
	}
}
