package azure

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"golang.org/x/sync/singleflight"
)

// cachingCredential memoizes access tokens per scope across every arm* client.
//
// disco builds a fresh SDK client per service/phase (180+ scanners); each
// gets its own azcore BearerTokenPolicy with an empty token cache, so its
// first request calls the underlying credential's GetToken — and
// DefaultAzureCredential.GetToken serializes under concurrency (~300ms
// each). With dozens of clients firing at once, that token acquisition — not
// the network or ARM — was the dominant scan-time cost (~18s for 60
// concurrent fresh clients vs ~0.8s through this cache). Wrapping the
// credential once and sharing it across clients turns each policy's first
// call into an instant cache hit.
type cachingCredential struct {
	inner azcore.TokenCredential
	mu    sync.Mutex
	toks  map[string]azcore.AccessToken
	// group coalesces concurrent cache-misses for the same scope into a single
	// inner fetch, so a cold cache (first service-loop burst) can't fan dozens
	// of simultaneous, serialized GetToken calls into the inner credential.
	group singleflight.Group
}

// newCachingCredential wraps inner so repeated GetToken calls for the same scope
// set reuse a still-valid token instead of re-invoking inner.
func newCachingCredential(inner azcore.TokenCredential) *cachingCredential {
	return &cachingCredential{inner: inner, toks: map[string]azcore.AccessToken{}}
}

// GetToken returns a cached token for the requested scopes if present and not
// within five minutes of expiry; otherwise it fetches from inner and caches
// the result. The five-minute skew matches azcore's own refresh window so
// callers never see a token the SDK would consider stale.
func (c *cachingCredential) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	key := tokenCacheKey(opts)
	c.mu.Lock()
	tok, ok := c.toks[key]
	c.mu.Unlock()
	if ok && time.Until(tok.ExpiresOn) > 5*time.Minute {
		return tok, nil
	}

	// Coalesce concurrent misses for the same scope; all scan callers share the
	// scan's root context, so the leader's ctx governing the fetch is fine.
	v, err, _ := c.group.Do(key, func() (any, error) {
		fresh, ferr := c.inner.GetToken(ctx, opts)
		if ferr != nil {
			return azcore.AccessToken{}, ferr
		}
		c.mu.Lock()
		c.toks[key] = fresh
		c.mu.Unlock()
		return fresh, nil
	})
	if err != nil {
		return azcore.AccessToken{}, err
	}
	return v.(azcore.AccessToken), nil
}

// tokenCacheKey derives a stable cache key from the token request. Scopes are
// the only field that changes across disco's callers (ARM vs Graph); tenant,
// when set, is folded in so a multi-tenant credential never crosses tokens.
func tokenCacheKey(opts policy.TokenRequestOptions) string {
	return opts.TenantID + "\x00" + strings.Join(opts.Scopes, " ")
}
