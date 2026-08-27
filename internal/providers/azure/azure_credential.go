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
// the result.
//
// The five minutes is this cache's OWN margin, and it is the ONLY one that
// decides what a caller gets. An earlier version of this comment claimed it
// matched azcore's; azcore's shouldRefresh applies its five-minute early window
// only when the token carries no RefreshOn (runtime/policy_bearer_token.go),
// and azidentity's confidential client copies RefreshOn from the MSAL result
// whenever the token response carried refresh_in (confidential_client.go copies
// ar.Metadata.RefreshOn, which MSAL fills only from that field) -- so which
// window azcore would use is not even fixed.
//
// It does not matter either way, and the reason is the thing to keep: azcore's
// refresh calls back into this method (runtime.acquire invokes the credential
// it was constructed with), so a refresh decision reads this map and gets the
// SAME token back while it is more than five minutes from expiry. This cache
// has no invalidation path, which is why the same token comes back on the NEXT
// call too. azcore's Expire() on a 401 is a separate fact needing no
// explanation from this cache: it is a method on the policy's own token state
// (BearerTokenPolicy.mainResource), and marking that state stale is all it
// does, so it could only ever clear the policy's copy. The policy IS wired to
// this credential -- that is the callback two sentences up -- which is why
// clearing its copy just fetches the same memoised token again. Read the
// credential the scan was handed before reasoning about token lifetime.
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
