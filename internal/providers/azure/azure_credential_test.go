package azure

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// countingCred is a fake inner credential that records how many times GetToken
// reached it and hands back a token with a caller-chosen lifetime.
type countingCred struct {
	calls   atomic.Int64
	expires time.Time
}

func (c *countingCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.calls.Add(1)
	return azcore.AccessToken{Token: "tok", ExpiresOn: c.expires}, nil
}

// blockingCred is a fake inner credential that blocks inside GetToken until
// release closes, signaling entry once via entered. Lets a test observe the
// deterministic in-flight window: while a leader is blocked inside the
// singleflight fn, no other goroutine can run it, so calls is provably 1
// regardless of how many callers are outstanding.
type blockingCred struct {
	calls   atomic.Int64
	expires time.Time
	once    sync.Once
	entered chan struct{} // closed when inner is first reached
	release chan struct{} // inner returns only after this is closed
}

func (c *blockingCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.calls.Add(1)
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return azcore.AccessToken{Token: "tok", ExpiresOn: c.expires}, nil
}

// TestCachingCredential_CoalescesByScope is the contract that makes Lever D
// work: many concurrent clients requesting the same scope must reach the inner
// (slow, serialized) credential at most once, while a different scope and an
// expiring token both force a fresh fetch.
func TestCachingCredential_CoalescesByScope(t *testing.T) {
	arm := policy.TokenRequestOptions{Scopes: []string{armScope}}

	// Concurrent same-scope callers coalesce: a blocked leader holds the
	// singleflight key, so the inner call count is deterministically 1 — no
	// follower can run the keyed fn. Asserting at this in-flight instant (vs
	// after the race resolves) removes the old flake where a follower that
	// read the cache empty before the leader populated it could start a
	// second fetch and push the count to 2–3.
	t.Run("concurrent same-scope coalesces", func(t *testing.T) {
		inner := &blockingCred{
			expires: time.Now().Add(time.Hour),
			entered: make(chan struct{}),
			release: make(chan struct{}),
		}
		c := newCachingCredential(inner)

		const n = 50
		var wg sync.WaitGroup
		toks := make([]azcore.AccessToken, n)
		errs := make([]error, n)
		for i := range n {
			wg.Go(func() {
				toks[i], errs[i] = c.GetToken(context.Background(), arm)
			})
		}

		<-inner.entered // a leader is inside the fn, blocked, holding the key
		if got := inner.calls.Load(); got != 1 {
			close(inner.release) // unblock leaked goroutines before failing
			t.Fatalf("in-flight: inner GetToken called %d times; want 1", got)
		}
		close(inner.release)
		wg.Wait()

		for i := range n {
			if errs[i] != nil {
				t.Errorf("caller %d: GetToken: %v", i, errs[i])
			}
			if toks[i].Token != "tok" {
				t.Errorf("caller %d: token = %q; want shared %q", i, toks[i].Token, "tok")
			}
		}
	})

	// Same scope cached, distinct scope refetched: a second same-scope call is a
	// cache hit; a different scope is a distinct key → fresh fetch.
	t.Run("same scope cached, distinct scope refetched", func(t *testing.T) {
		inner := &countingCred{expires: time.Now().Add(time.Hour)}
		c := newCachingCredential(inner)
		if _, err := c.GetToken(context.Background(), arm); err != nil {
			t.Fatalf("GetToken arm(1): %v", err)
		}
		if _, err := c.GetToken(context.Background(), arm); err != nil {
			t.Fatalf("GetToken arm(2): %v", err)
		}
		if got := inner.calls.Load(); got != 1 {
			t.Errorf("same scope cached: inner GetToken called %d times; want 1", got)
		}

		graph := policy.TokenRequestOptions{Scopes: []string{"https://graph.microsoft.com/.default"}}
		if _, err := c.GetToken(context.Background(), graph); err != nil {
			t.Fatalf("GetToken graph: %v", err)
		}
		if got := inner.calls.Load(); got != 2 {
			t.Errorf("distinct scope: inner GetToken called %d times; want 2", got)
		}
	})

	// A cached token within the 5-minute skew is treated as stale → refetch.
	t.Run("near-expiry token refetched", func(t *testing.T) {
		inner := &countingCred{expires: time.Now().Add(time.Minute)}
		c := newCachingCredential(inner)
		if _, err := c.GetToken(context.Background(), arm); err != nil {
			t.Fatalf("GetToken expiring(1): %v", err)
		}
		if _, err := c.GetToken(context.Background(), arm); err != nil {
			t.Fatalf("GetToken expiring(2): %v", err)
		}
		if got := inner.calls.Load(); got != 2 {
			t.Errorf("near-expiry token: inner GetToken called %d times; want 2 (no caching of stale tokens)", got)
		}
	})
}
