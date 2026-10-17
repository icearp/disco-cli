package aws

import (
	"context"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/credentials"
	smithymw "github.com/aws/smithy-go/middleware"
)

// stubCall is one canned response in the middleware stub queue. Output must be
// the SDK's *<Op>Output pointer for the operation. Err is returned to the
// caller; when non-nil the SDK retryer/error-classifier still runs against
// it — why this harness beats interface mocks for retry/transient
// classification tests.
type stubCall struct {
	Output any
	Err    error
}

// stubResponses returns an APIOption that injects an Initialize-step middleware
// short-circuiting requests with canned responses. responses is keyed by SDK
// operation name (e.g. "ListQueues", "GetQueueAttributes"); each stubCall
// slice is consumed in order — the Nth call to an op pops its Nth entry,
// supporting multi-page paginator state without per-page reconstruction. Per
// AWS SDK Go v2 unit-testing guide §"Using middleware".
//
// Calling an op with no queued response, or exhausting the queue, fails the
// test via t.Fatalf so silent over-/under-call regressions surface.
func stubResponses(t *testing.T, responses map[string][]stubCall) func(*smithymw.Stack) error {
	t.Helper()
	type opQueue struct {
		mu    sync.Mutex
		queue []stubCall
		idx   int
	}
	state := make(map[string]*opQueue, len(responses))
	for op, calls := range responses {
		state[op] = &opQueue{queue: calls}
	}
	return func(s *smithymw.Stack) error {
		return s.Initialize.Add(smithymw.InitializeMiddlewareFunc("discoTestStub",
			func(ctx context.Context, _ smithymw.InitializeInput, _ smithymw.InitializeHandler) (smithymw.InitializeOutput, smithymw.Metadata, error) {
				op := awsmw.GetOperationName(ctx)
				q, ok := state[op]
				if !ok {
					t.Fatalf("stubResponses: no queue registered for op %q", op)
					return smithymw.InitializeOutput{}, smithymw.Metadata{}, nil
				}
				q.mu.Lock()
				defer q.mu.Unlock()
				if q.idx >= len(q.queue) {
					t.Fatalf("stubResponses: queue for op %q exhausted at call %d", op, q.idx+1)
					return smithymw.InitializeOutput{}, smithymw.Metadata{}, nil
				}
				c := q.queue[q.idx]
				q.idx++
				return smithymw.InitializeOutput{Result: c.Output}, smithymw.Metadata{}, c.Err
			}), smithymw.After)
	}
}

// cfgWithStub is an SDK config whose every call is answered by stub (usually
// stubResponses), with static credentials and a single attempt so a stubbed
// error reaches the scanner unretried.
func cfgWithStub(stub func(*smithymw.Stack) error, region string) sdkaws.Config {
	return sdkaws.Config{
		Region:           region,
		Credentials:      credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
		RetryMaxAttempts: 1,
		APIOptions:       []func(*smithymw.Stack) error{stub},
	}
}

// opStubConfig is an SDK config whose calls are answered by handlers keyed by
// operation name, each given the op's input. An op without a handler fails with
// denyErr, as for a role lacking that action. Handlers can run on fan-out
// goroutines, so any state they share must be locked.
func opStubConfig(handlers map[string]func(in any) (any, error), denyErr error) sdkaws.Config {
	return opStubConfigCtx(ignoreCtx(handlers), denyErr)
}

// ignoreCtx adapts context-free stub handlers to opStubConfigCtx.
func ignoreCtx(handlers map[string]func(in any) (any, error)) map[string]func(context.Context, any) (any, error) {
	withCtx := make(map[string]func(context.Context, any) (any, error), len(handlers))
	for op, h := range handlers {
		withCtx[op] = func(_ context.Context, in any) (any, error) { return h(in) }
	}
	return withCtx
}

// opStubConfigCtx is opStubConfig for handlers that need the call's context,
// e.g. to block until a fan-out cancels its siblings.
func opStubConfigCtx(handlers map[string]func(ctx context.Context, in any) (any, error), denyErr error) sdkaws.Config {
	stub := func(s *smithymw.Stack) error {
		return s.Initialize.Add(smithymw.InitializeMiddlewareFunc("discoOpStub",
			func(ctx context.Context, in smithymw.InitializeInput, _ smithymw.InitializeHandler) (smithymw.InitializeOutput, smithymw.Metadata, error) {
				h, ok := handlers[awsmw.GetOperationName(ctx)]
				if !ok {
					return smithymw.InitializeOutput{}, smithymw.Metadata{}, denyErr
				}
				out, err := h(ctx, in.Parameters)
				return smithymw.InitializeOutput{Result: out}, smithymw.Metadata{}, err
			}), smithymw.After)
	}
	return cfgWithStub(stub, testRegion)
}
