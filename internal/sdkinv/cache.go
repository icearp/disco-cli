package sdkinv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	Provider string `json:"provider"`
	Ref      string `json:"ref"`
	// Spec fingerprints the FetchSpec the snapshot was fetched with. The ref
	// alone does not identify the files on disk: widening a source's Keep
	// filter at an unchanged ref leaves every existing cache holding the old,
	// narrower file set, which the extractor reads as a smaller universe with
	// no fetch and no warning. Empty in snapshots written before the field
	// existed, which Status treats as a mismatch.
	Spec      string         `json:"spec"`
	FetchedAt time.Time      `json:"fetchedAt"`
	Sources   []SourceRecord `json:"sources"`
}

// SpecFingerprint hashes everything about a FetchSpec that decides which files
// land on disk. Keep and Expand are funcs and cannot be hashed, so KeepID and
// ExpandID stand in for them; a change that does not move those ids is
// invisible here.
func SpecFingerprint(spec []FetchSource) string {
	var b strings.Builder
	for _, s := range spec {
		b.WriteString(strings.Join([]string{
			s.Name, string(s.Kind), s.URL, s.Dest, strconv.Itoa(s.Strip), s.KeepID, s.ExpandID,
			strconv.FormatBool(s.Keep != nil), strconv.FormatBool(s.LocalDir != ""),
			strconv.FormatBool(s.Expand != nil),
		}, "|"))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:16]
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

// Status loads the manifest for e's snapshot, or ErrNotFetched. A snapshot
// whose manifest names a different provider/ref, or was fetched with a
// different FetchSpec, is reported as not fetched: the files on disk are not
// the ones e would read now, and refetching is the only way to find out.
func (c Cache) Status(e Extractor) (*Manifest, error) {
	provider, ref := e.Name(), e.Ref()
	notFetched := func(why string) error {
		return fmt.Errorf("%w: %s@%s%s (run: disco coverage sdk fetch --providers %s)", ErrNotFetched, provider, ref, why, provider)
	}
	raw, err := os.ReadFile(filepath.Join(c.Dir(provider, ref), manifestFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, notFetched("")
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s manifest: %w", provider, err)
	}
	if m.Provider != provider || m.Ref != ref {
		return nil, notFetched(fmt.Sprintf(" (manifest says %s@%s)", m.Provider, m.Ref))
	}
	if want := SpecFingerprint(e.FetchSpec()); m.Spec != want {
		return nil, notFetched(fmt.Sprintf(" (fetched with a different source spec: %s, now %s)", orNone(m.Spec), want))
	}
	return &m, nil
}

func orNone(s string) string {
	if s == "" {
		return "none recorded"
	}
	return s
}

// EnsureOptions tune Ensure.
type EnsureOptions struct {
	Force  bool         // refetch even when the snapshot is present
	Client *http.Client // nil = fetchClient(): per-hop timeouts, no overall deadline (large archives)
	Log    func(format string, args ...any)
}

// Ensure returns the populated snapshot dir for e, fetching it when absent or
// forced. The fetch lands in a temp dir beside the target and is renamed into
// place only after every source succeeded and the manifest is written, so a
// partial download is never observable as a snapshot.
func (c Cache) Ensure(ctx context.Context, e Extractor, opts EnsureOptions) (dir string, fetched bool, err error) {
	dir = c.Dir(e.Name(), e.Ref())
	if !opts.Force {
		if _, serr := c.Status(e); serr == nil {
			return dir, false, nil
		}
	}
	client := opts.Client
	if client == nil {
		client = fetchClient()
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

	m := Manifest{Provider: e.Name(), Ref: e.Ref(), Spec: SpecFingerprint(e.FetchSpec()), FetchedAt: time.Now().UTC()}
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
		if _, serr := c.Status(e); serr == nil {
			return dir, false, nil
		}
		return "", false, err
	}
	return dir, true, nil
}
