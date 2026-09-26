package sdkinv

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// maxEntryBytes caps one extracted file so a hostile or corrupt archive
// cannot fill the disk; the largest legitimate input (the EC2 Smithy model) is
// under 10MB.
const maxEntryBytes = 64 << 20

// indexFetchConcurrency bounds JSON-index fan-out; the AWS Service Reference
// is a static CDN of small docs, so this is latency-bound.
const indexFetchConcurrency = 32

const userAgent = "disco-coverage-sdk-fetch"

// Per-hop limits for the fetch client. No overall Client.Timeout: see
// idleReader.
const (
	bodyIdleTimeout       = 60 * time.Second
	responseHeaderTimeout = 45 * time.Second
	tlsHandshakeTimeout   = 20 * time.Second
)

// fetchClient bounds the stalls a cache fetch can hit without bounding the
// transfer itself.
func fetchClient() *http.Client {
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{}
	}
	t := tr.Clone()
	t.ResponseHeaderTimeout = responseHeaderTimeout
	t.TLSHandshakeTimeout = tlsHandshakeTimeout
	return &http.Client{Transport: t}
}

// safeEntryName bounds JSON-index entry names to one plain path segment:
// no separators, no dot-only names, bounded length.
// indexName is the index document's own file name; entries are written beside
// it as <name>.json, so no entry may be called this.
const indexName = "index"

var safeEntryName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$`)

func fetchSource(ctx context.Context, client *http.Client, src FetchSource, dest string) (SourceRecord, error) {
	rec := SourceRecord{Name: src.Name, Kind: src.Kind, URL: src.URL}
	if src.LocalDir != "" {
		if st, err := os.Stat(src.LocalDir); err == nil && st.IsDir() {
			n, err := copyLocal(src.LocalDir, dest, src.Keep)
			rec.Files = n
			rec.URL = "file://" + src.LocalDir
			return rec, err
		}
	}
	switch src.Kind {
	case KindTarball:
		return fetchTarball(ctx, client, src, dest, rec)
	case KindModuleZip:
		return fetchModuleZip(ctx, client, src, dest, rec)
	case KindJSONIndex:
		return fetchJSONIndex(ctx, client, src, dest, rec)
	default:
		return rec, fmt.Errorf("unknown fetch kind %q", src.Kind)
	}
}

// httpGet opens url and returns the body; the caller closes it. The body is
// wrapped so a transfer that stops producing bytes fails instead of hanging
// until the process is killed.
func httpGet(ctx context.Context, client *http.Client, url string) (io.ReadCloser, error) {
	return httpGetIdle(ctx, client, url, bodyIdleTimeout)
}

func httpGetIdle(ctx context.Context, client *http.Client, url string, idle time.Duration) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return newIdleReader(resp.Body, cancel, idle), nil
}

// idleReader cancels the request when no byte arrives for timeout. A whole-
// request deadline cannot do this job: the AWS tarball is several hundred MB
// and a legitimate slow link would trip it, while a server that stalls
// mid-body trips nothing at all.
type idleReader struct {
	rc      io.ReadCloser
	cancel  context.CancelFunc
	timer   *time.Timer
	d       time.Duration
	stalled atomic.Bool
}

func newIdleReader(rc io.ReadCloser, cancel context.CancelFunc, d time.Duration) *idleReader {
	ir := &idleReader{rc: rc, cancel: cancel, d: d}
	// The timer fires the cancel, which makes the in-flight Read return; a
	// Reset racing a fire is harmless because the context stays cancelled.
	ir.timer = time.AfterFunc(d, func() { ir.stalled.Store(true); cancel() })
	return ir
}

func (ir *idleReader) Read(p []byte) (int, error) {
	n, err := ir.rc.Read(p)
	switch {
	case n > 0:
		ir.timer.Reset(ir.d)
	case err != nil && ir.stalled.Load():
		// Without this the fetch reports a bare "context canceled", which
		// reads as an interrupted run rather than a server that stopped.
		err = fmt.Errorf("transfer stalled: no data for %s: %w", ir.d, err)
	}
	return n, err
}

func (ir *idleReader) Close() error {
	ir.timer.Stop()
	err := ir.rc.Close()
	ir.cancel()
	return err
}

// hashingReader tees bytes into a sha256 while counting them.
type hashingReader struct {
	r io.Reader
	h io.Writer
	n int64
}

func (hr *hashingReader) Read(p []byte) (int, error) {
	n, err := hr.r.Read(p)
	if n > 0 {
		_, _ = hr.h.Write(p[:n])
		hr.n += int64(n)
	}
	return n, err
}

func fetchTarball(ctx context.Context, client *http.Client, src FetchSource, dest string, rec SourceRecord) (SourceRecord, error) {
	body, err := httpGet(ctx, client, src.URL)
	if err != nil {
		return rec, err
	}
	defer body.Close()
	sum := sha256.New()
	hr := &hashingReader{r: body, h: sum}
	gz, err := gzip.NewReader(hr)
	if err != nil {
		return rec, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return rec, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		rel, ok := archivePath(hdr.Name, src.Strip, src.Keep)
		if !ok {
			continue
		}
		if err := writeEntry(dest, rel, tr, hdr.Size); err != nil {
			return rec, err
		}
		rec.Files++
	}
	rec.SHA256 = hex.EncodeToString(sum.Sum(nil))
	rec.Bytes = hr.n
	return rec, nil
}

func fetchModuleZip(ctx context.Context, client *http.Client, src FetchSource, dest string, rec SourceRecord) (SourceRecord, error) {
	body, err := httpGet(ctx, client, src.URL)
	if err != nil {
		return rec, err
	}
	defer body.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return rec, err
	}
	// archive/zip needs random access: spool to a temp file beside dest,
	// which lives inside the snapshot temp dir and is discarded with it.
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".zip-*")
	if err != nil {
		return rec, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, sum), body)
	if err != nil {
		return rec, err
	}
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		return rec, err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel, ok := archivePath(f.Name, src.Strip, src.Keep)
		if !ok {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return rec, err
		}
		werr := writeEntry(dest, rel, rc, int64(f.UncompressedSize64))
		rc.Close()
		if werr != nil {
			return rec, werr
		}
		rec.Files++
	}
	rec.SHA256 = hex.EncodeToString(sum.Sum(nil))
	rec.Bytes = n
	return rec, nil
}

func fetchJSONIndex(ctx context.Context, client *http.Client, src FetchSource, dest string, rec SourceRecord) (SourceRecord, error) {
	if src.Expand == nil {
		return rec, errors.New("json-index source has no Expand")
	}
	body, err := httpGet(ctx, client, src.URL)
	if err != nil {
		return rec, err
	}
	index, err := io.ReadAll(io.LimitReader(body, maxEntryBytes))
	_, _ = io.Copy(io.Discard, body) // drain so the connection can be reused
	body.Close()
	if err != nil {
		return rec, err
	}
	sum := sha256.Sum256(index)
	rec.SHA256 = hex.EncodeToString(sum[:])
	rec.Bytes = int64(len(index))
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return rec, err
	}
	if err := os.WriteFile(filepath.Join(dest, indexName+".json"), index, 0o644); err != nil {
		return rec, err
	}
	rec.Files = 1
	entries, err := src.Expand(index)
	if err != nil {
		return rec, err
	}
	// Validate the whole index before any fetch starts. Rejecting mid-loop
	// returned while the goroutines already launched kept writing into the
	// snapshot the caller was about to discard.
	seen := make(map[string]bool, len(entries))
	for _, en := range entries {
		if en.URL == "" || !safeEntryName.MatchString(en.Name) {
			return rec, fmt.Errorf("index entry %q (%s) is not a safe file name", en.Name, en.URL)
		}
		if en.Name == indexName {
			return rec, fmt.Errorf("index entry %q would overwrite the index document itself", en.Name)
		}
		if seen[en.Name] {
			return rec, fmt.Errorf("index entry %q appears twice", en.Name)
		}
		seen[en.Name] = true
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(indexFetchConcurrency)
	for _, en := range entries {
		g.Go(func() error {
			b, err := httpGet(gctx, client, en.URL)
			if err != nil {
				return err
			}
			defer b.Close()
			return writeEntry(dest, en.Name+".json", b, -1)
		})
	}
	if err := g.Wait(); err != nil {
		return rec, err
	}
	rec.Files += len(entries)
	return rec, nil
}

// archivePath strips leading components, rejects unsafe names and applies Keep.
func archivePath(name string, strip int, keep func(string) bool) (string, bool) {
	// Archives may carry backslash separators; path.Clean only understands "/".
	clean := path.Clean(strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "./"))
	if path.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	parts := strings.Split(clean, "/")
	if len(parts) <= strip {
		return "", false
	}
	rel := strings.Join(parts[strip:], "/")
	if keep != nil && !keep(rel) {
		return "", false
	}
	return rel, true
}

// writeEntry streams one archive entry (or HTTP body) to dest/rel, capped at
// maxEntryBytes. size < 0 means unknown.
func writeEntry(dest, rel string, r io.Reader, size int64) error {
	if size > maxEntryBytes {
		return fmt.Errorf("%s: %d bytes exceeds cap", rel, size)
	}
	p := filepath.Join(dest, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxEntryBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxEntryBytes {
		return fmt.Errorf("%s: exceeds %d byte cap", rel, maxEntryBytes)
	}
	return nil
}

// copyLocal mirrors a local tree (e.g. GOMODCACHE) into dest through Keep.
func copyLocal(root, dest string, keep func(string) bool) (int, error) {
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if keep != nil && !keep(rel) {
			return nil
		}
		f, oerr := os.Open(p)
		if oerr != nil {
			return oerr
		}
		defer f.Close()
		if werr := writeEntry(dest, rel, f, -1); werr != nil {
			return werr
		}
		n++
		return nil
	})
	return n, err
}
