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

// TestCachingCredential_CoalescesByScope is the contract that makes Lever D
// work: many concurrent clients requesting the same scope must reach the inner
// (slow, serialized) credential at most once, while a different scope and an
// expiring token both force a fresh fetch.
func TestCachingCredential_CoalescesByScope(t *testing.T) {
	inner := &countingCred{expires: time.Now().Add(time.Hour)}
	c := newCachingCredential(inner)
	arm := policy.TokenRequestOptions{Scopes: []string{"https://management.azure.com/.default"}}

	// 50 concurrent fetches for the same scope → inner hit at most once.
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if _, err := c.GetToken(context.Background(), arm); err != nil {
				t.Errorf("GetToken: %v", err)
			}
		})
	}
	wg.Wait()
	if got := inner.calls.Load(); got != 1 {
		t.Errorf("same-scope concurrent: inner GetToken called %d times; want 1", got)
	}

	// A different scope is a distinct cache key → one more inner call.
	graph := policy.TokenRequestOptions{Scopes: []string{"https://graph.microsoft.com/.default"}}
	if _, err := c.GetToken(context.Background(), graph); err != nil {
		t.Fatalf("GetToken graph: %v", err)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Errorf("distinct scope: inner GetToken called %d times; want 2", got)
	}

	// A cached token within the 5-minute skew is treated as stale → refetch.
	expiring := &countingCred{expires: time.Now().Add(time.Minute)}
	c2 := newCachingCredential(expiring)
	if _, err := c2.GetToken(context.Background(), arm); err != nil {
		t.Fatalf("GetToken expiring(1): %v", err)
	}
	if _, err := c2.GetToken(context.Background(), arm); err != nil {
		t.Fatalf("GetToken expiring(2): %v", err)
	}
	if got := expiring.calls.Load(); got != 2 {
		t.Errorf("near-expiry token: inner GetToken called %d times; want 2 (no caching of stale tokens)", got)
	}
}
