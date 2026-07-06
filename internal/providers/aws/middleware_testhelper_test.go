package aws

import (
	"context"
	"sync"
	"testing"

	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
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
