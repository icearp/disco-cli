package sdkinv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/adrg/xdg"
)

// manifestFile marks a complete provider snapshot; a dir without it is treated
// as absent so an interrupted fetch never masquerades as a populated cache.
const manifestFile = "manifest.json"

// Cache is the on-disk SDK source store, one subdirectory per <provider>@<ref>.
type Cache struct {
	Root string
}

// DefaultCacheRoot is $XDG_CACHE_HOME/disco/sdk (macOS/Windows: the platform
// cache dir), kept apart from the config and data dirs cmd/paths.go resolves.
func DefaultCacheRoot() string {
	return filepath.Join(xdg.CacheHome, "disco", "sdk")
}

// Manifest describes one fetched snapshot.
type Manifest struct {
	Provider  string         `json:"provider"`
	Ref       string         `json:"ref"`
	FetchedAt time.Time      `json:"fetchedAt"`
	Sources   []SourceRecord `json:"sources"`
}

// SourceRecord is the fetch result for one FetchSource.
type SourceRecord struct {
	Name   string `json:"name"`
	Kind   Kind   `json:"kind"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"` // of the downloaded archive/index; empty when copied locally
	Bytes  int64  `json:"bytes"`            // downloaded bytes
	Files  int    `json:"files"`            // files written
}

// ErrNotFetched reports an absent or incomplete provider snapshot.
var ErrNotFetched = errors.New("sdk cache not populated")

// Dir returns the snapshot directory for a provider ref.
func (c Cache) Dir(provider, ref string) string {
	return filepath.Join(c.Root, provider+"@"+ref)
}

// Status loads the manifest for a provider ref, or ErrNotFetched.
func (c Cache) Status(provider, ref string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(c.Dir(provider, ref), manifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s@%s (run: disco coverage sdk fetch --providers %s)", ErrNotFetched, provider, ref, provider)
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s manifest: %w", provider, err)
	}
	return &m, nil
}

// EnsureOptions tune Ensure.
type EnsureOptions struct {
	Force  bool         // refetch even when the snapshot is present
	Client *http.Client // nil = a client with no overall timeout (large archives); ctx governs cancellation
	Log    func(format string, args ...any)
}

// Ensure returns the populated snapshot dir for e, fetching it when absent or
// forced. The fetch lands in a temp dir beside the target and is renamed into
// place only after every source succeeded and the manifest is written, so a
// partial download is never observable as a snapshot.
func (c Cache) Ensure(ctx context.Context, e Extractor, opts EnsureOptions) (dir string, fetched bool, err error) {
	dir = c.Dir(e.Name(), e.Ref())
	if !opts.Force {
		if _, serr := c.Status(e.Name(), e.Ref()); serr == nil {
			return dir, false, nil
		}
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{}
	}
	logf := opts.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if err := os.MkdirAll(c.Root, 0o755); err != nil {
		return "", false, err
	}
	tmp, err := os.MkdirTemp(c.Root, ".tmp-"+e.Name()+"-")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(tmp)

	m := Manifest{Provider: e.Name(), Ref: e.Ref(), FetchedAt: time.Now().UTC()}
	for _, src := range e.FetchSpec() {
		logf("%s: fetching %s", e.Name(), src.Name)
		rec, ferr := fetchSource(ctx, client, src, filepath.Join(tmp, src.Dest))
		if ferr != nil {
			return "", false, fmt.Errorf("%s %s: %w", e.Name(), src.Name, ferr)
		}
		logf("%s: %s — %d files", e.Name(), src.Name, rec.Files)
		m.Sources = append(m.Sources, rec)
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", false, err
	}
	if err := os.WriteFile(filepath.Join(tmp, manifestFile), raw, 0o644); err != nil {
		return "", false, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", false, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		// Two fetches of the same snapshot can race here; if the other one
		// already renamed a complete snapshot into place, ours is redundant.
		if _, serr := c.Status(e.Name(), e.Ref()); serr == nil {
			return dir, false, nil
		}
		return "", false, err
	}
	return dir, true, nil
}
