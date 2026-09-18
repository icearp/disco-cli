package sdkinv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

type fakeExtractor struct {
	name, ref string
	spec      []FetchSource
}

func (f fakeExtractor) Name() string             { return f.name }
func (f fakeExtractor) Ref() string              { return f.ref }
func (f fakeExtractor) FetchSpec() []FetchSource { return f.spec }
func (fakeExtractor) Extract(context.Context, string) (*Universe, error) {
	return nil, ErrNotImplemented
}

func TestCacheEnsure_FetchesOnceThenNoOp(t *testing.T) {
	tgz := buildTarGz(t, []tarEntry{{"repo-x/a/one.json", "{}"}, {"repo-x/a/two.json", "{}"}})
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++; _, _ = w.Write(tgz) }))
	defer srv.Close()
	c := Cache{Root: filepath.Join(t.TempDir(), "sdk")}
	e := fakeExtractor{name: "fake", ref: "r1", spec: []FetchSource{{Name: "models", Kind: KindTarball, URL: srv.URL, Dest: "m", Strip: 1}}}
	opts := EnsureOptions{Client: srv.Client()}

	if _, err := c.Status(e); !errors.Is(err, ErrNotFetched) {
		t.Fatalf("Status before fetch = %v, want ErrNotFetched", err)
	}
	dir, fetched, err := c.Ensure(context.Background(), e, opts)
	if err != nil || !fetched || hits != 1 {
		t.Fatalf("first Ensure: dir=%s fetched=%v hits=%d err=%v", dir, fetched, hits, err)
	}
	if dir != c.Dir("fake", "r1") {
		t.Fatalf("dir = %s", dir)
	}
	m, err := c.Status(e)
	if err != nil || len(m.Sources) != 1 || m.Sources[0].Files != 2 || m.Ref != "r1" {
		t.Fatalf("manifest = %+v err=%v", m, err)
	}
	if got := listFiles(t, filepath.Join(dir, "m")); len(got) != 2 {
		t.Fatalf("files = %v", got)
	}

	if _, fetched, err := c.Ensure(context.Background(), e, opts); err != nil || fetched || hits != 1 {
		t.Fatalf("second Ensure: fetched=%v hits=%d err=%v", fetched, hits, err)
	}
	if _, fetched, err := c.Ensure(context.Background(), e, EnsureOptions{Client: srv.Client(), Force: true}); err != nil || !fetched || hits != 2 {
		t.Fatalf("forced Ensure: fetched=%v hits=%d err=%v", fetched, hits, err)
	}
	// A different ref is a different snapshot; the old one is untouched.
	e2 := fakeExtractor{name: "fake", ref: "r2", spec: e.spec}
	if _, fetched, err := c.Ensure(context.Background(), e2, opts); err != nil || !fetched {
		t.Fatalf("ref2 Ensure: fetched=%v err=%v", fetched, err)
	}
	if _, err := c.Status(e); err != nil {
		t.Fatalf("r1 lost after r2 fetch: %v", err)
	}
	// No temp dirs linger.
	entries, _ := os.ReadDir(c.Root)
	for _, en := range entries {
		if en.Name()[0] == '.' {
			t.Fatalf("temp dir left behind: %s", en.Name())
		}
	}
}

// A snapshot fetched under a different FetchSpec holds a different file set,
// which the extractor would silently read as a different universe.
func TestCacheStatus_SpecChangeIsNotFetched(t *testing.T) {
	tgz := buildTarGz(t, []tarEntry{{"repo-x/a/one.json", "{}"}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tgz) }))
	defer srv.Close()
	c := Cache{Root: filepath.Join(t.TempDir(), "sdk")}
	narrow := fakeExtractor{name: "fake", ref: "r1", spec: []FetchSource{
		{Name: "models", Kind: KindTarball, URL: srv.URL, Dest: "m", Strip: 1, Keep: func(string) bool { return true }, KeepID: "narrow"},
	}}
	if _, _, err := c.Ensure(context.Background(), narrow, EnsureOptions{Client: srv.Client()}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	wide := narrow
	wide.spec = []FetchSource{{Name: "models", Kind: KindTarball, URL: srv.URL, Dest: "m", Strip: 1, Keep: func(string) bool { return true }, KeepID: "wide"}}
	if _, err := c.Status(wide); !errors.Is(err, ErrNotFetched) {
		t.Fatalf("Status after KeepID change = %v, want ErrNotFetched", err)
	}
	// And Ensure refetches rather than reporting the stale snapshot present.
	if _, fetched, err := c.Ensure(context.Background(), wide, EnsureOptions{Client: srv.Client()}); err != nil || !fetched {
		t.Fatalf("Ensure after KeepID change: fetched=%v err=%v", fetched, err)
	}
	if _, err := c.Status(wide); err != nil {
		t.Fatalf("Status after refetch: %v", err)
	}
}

// A manifest naming another provider or ref is not this extractor's snapshot.
func TestCacheStatus_ManifestIdentityMismatch(t *testing.T) {
	c := Cache{Root: t.TempDir()}
	e := fakeExtractor{name: "fake", ref: "r1"}
	dir := c.Dir("fake", "r1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(Manifest{Provider: "fake", Ref: "r0", Spec: SpecFingerprint(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifestFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(e); !errors.Is(err, ErrNotFetched) {
		t.Fatalf("Status on a foreign manifest = %v, want ErrNotFetched", err)
	}
}

func TestCacheEnsure_FailureLeavesNoSnapshot(t *testing.T) {
	tgz := buildTarGz(t, []tarEntry{{"repo-x/a/one.json", "{}"}})
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tgz) })
	mux.HandleFunc("/bad", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := Cache{Root: filepath.Join(t.TempDir(), "sdk")}
	e := fakeExtractor{name: "fake", ref: "r1", spec: []FetchSource{
		{Name: "good", Kind: KindTarball, URL: srv.URL + "/ok", Dest: "g", Strip: 1},
		{Name: "bad", Kind: KindTarball, URL: srv.URL + "/bad", Dest: "b", Strip: 1},
	}}
	if _, _, err := c.Ensure(context.Background(), e, EnsureOptions{Client: srv.Client()}); err == nil {
		t.Fatal("expected failure")
	}
	if _, err := c.Status(e); !errors.Is(err, ErrNotFetched) {
		t.Fatalf("partial snapshot visible: %v", err)
	}
	if _, err := os.Stat(c.Dir("fake", "r1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot dir exists after failure: %v", err)
	}
	entries, _ := os.ReadDir(c.Root)
	if len(entries) != 0 {
		t.Fatalf("leftovers after failure: %v", entries)
	}
}

// dupTestSeq keeps the name unique per run: the registry is package state, so
// a second run of this test in the same process would otherwise panic on the
// first Register (`go test -count=2`).
var dupTestSeq atomic.Int64

func TestRegistry_DuplicatePanics(t *testing.T) {
	name := fmt.Sprintf("dup-test-%d", dupTestSeq.Add(1))
	Register(fakeExtractor{name: name, ref: "x"})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Register did not panic")
		}
	}()
	Register(fakeExtractor{name: name, ref: "y"})
}
