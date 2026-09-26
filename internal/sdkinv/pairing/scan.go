package pairing

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// Scan extracts provider's universe from the SDK cache, walks the scanner
// package at dir and reports the pairings and the unpaired type constants.
// Returns sdkinv.ErrNotFetched (wrapped) when the cache lacks the provider.
func Scan(ctx context.Context, cache sdkinv.Cache, provider, dir string) (*Result, map[string]string, error) {
	e, ok := sdkinv.Get(provider)
	if !ok {
		return nil, nil, fmt.Errorf("pairing: no extractor for provider %q", provider)
	}
	if _, err := cache.Status(e); err != nil {
		return nil, nil, err
	}
	u, err := e.Extract(ctx, cache.Dir(provider, e.Ref()))
	if err != nil {
		return nil, nil, err
	}
	res, err := Walk(dir, u)
	if err != nil {
		return nil, nil, err
	}
	r, _ := Get(provider)
	sdk, err := SDKFiles(dir, r)
	if err != nil {
		return nil, nil, err
	}
	return res, res.Unpaired(res.Consts, sdk), nil
}
