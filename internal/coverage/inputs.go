package coverage

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/icearp/disco-cli/internal/sdkinv/pairing"
)

// InputsFromCache derives a provider's universe from the SDK source cache
// and, when scannerDir names the provider's scanner package, pairs it with
// the scanners. Errors wrap sdkinv.ErrNotFetched when the cache is absent so
// callers can hint at `disco coverage sdk fetch`.
func InputsFromCache(ctx context.Context, cache sdkinv.Cache, provider string, emits []TypeDecl, scannerDir string) (Inputs, error) {
	in := Inputs{Provider: provider, Emits: emits}
	e, ok := sdkinv.Get(provider)
	if !ok {
		return in, fmt.Errorf("no SDK extractor for provider %s", provider)
	}
	if _, err := cache.Status(e); err != nil {
		return in, err
	}
	u, err := e.Extract(ctx, cache.Dir(provider, e.Ref()))
	if err != nil {
		return in, err
	}
	in.Universe = u
	if scannerDir == "" {
		return in, nil
	}
	res, err := pairing.Walk(scannerDir, u)
	if err != nil {
		return in, fmt.Errorf("pair %s scanners under %s: %w", provider, scannerDir, err)
	}
	r, _ := pairing.Get(provider)
	sdk, err := pairing.SDKFiles(scannerDir, r)
	if err != nil {
		return in, err
	}
	in.Pairings, in.Unpaired, in.Paired = res.Pairings, res.Unpaired(res.Consts, sdk), true
	return in, nil
}
