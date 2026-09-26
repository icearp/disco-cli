package sdkinv

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type tarEntry struct {
	name string
	body string
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func TestArchivePath(t *testing.T) {
	keepJSON := func(p string) bool { return filepath.Ext(p) == ".json" }
	cases := []struct {
		name  string
		strip int
		want  string
		ok    bool
	}{
		{"repo-ref/codegen/a.json", 1, "codegen/a.json", true},
		{"./repo-ref/codegen/a.json", 1, "codegen/a.json", true},
		{"repo-ref/codegen/a.go", 1, "", false},
		{"repo-ref", 1, "", false},
		{"../evil.json", 0, "", false},
		{"repo-ref/../../evil.json", 1, "", false},
		{"/abs/evil.json", 0, "", false},
		{`repo-ref\..\..\evil.json`, 1, "", false},
		{`repo-ref\codegen\b.json`, 1, "codegen/b.json", true},
		{"google.golang.org/api@v1/compute/v1/compute-api.json", 2, "compute/v1/compute-api.json", true},
	}
	for _, c := range cases {
		got, ok := archivePath(c.name, c.strip, keepJSON)
		if ok != c.ok || got != c.want {
			t.Errorf("archivePath(%q, %d) = (%q, %v), want (%q, %v)", c.name, c.strip, got, ok, c.want, c.ok)
		}
	}
}

func TestFetchTarball_FiltersAndStrips(t *testing.T) {
	tgz := buildTarGz(t, []tarEntry{
		{"repo-abc/codegen/sdk-codegen/aws-models/ec2.json", `{"ec2":1}`},
		{"repo-abc/codegen/sdk-codegen/aws-models/README.md", "no"},
		{"repo-abc/service/ec2/api_client.go", "package ec2"},
		{"repo-abc/../escape.json", "evil"},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tgz) }))
	defer srv.Close()
	dest := t.TempDir()
	src := FetchSource{Name: "models", Kind: KindTarball, URL: srv.URL, Strip: 1, Keep: func(p string) bool {
		return filepath.Ext(p) == ".json"
	}}
	rec, err := fetchSource(context.Background(), srv.Client(), src, dest)
	if err != nil {
		t.Fatal(err)
	}
	got := listFiles(t, dest)
	want := []string{"codegen/sdk-codegen/aws-models/ec2.json"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("files = %v, want %v", got, want)
	}
	if rec.Files != 1 || rec.Bytes != int64(len(tgz)) || len(rec.SHA256) != 64 {
		t.Fatalf("record = %+v", rec)
	}
	if _, err := os.Stat(filepath.Join(dest, "..", "escape.json")); err == nil {
		t.Fatal("path traversal entry was written")
	}
}

func TestFetchModuleZip_StripsModulePrefix(t *testing.T) {
	z := buildZip(t, []tarEntry{
		{"google.golang.org/api@v0.1.0/compute/v1/compute-api.json", `{}`},
		{"google.golang.org/api@v0.1.0/compute/v1/compute-gen.go", "package compute"},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(z) }))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "api")
	src := FetchSource{Name: "docs", Kind: KindModuleZip, URL: srv.URL, Strip: 2, Keep: func(p string) bool {
		return filepath.Ext(p) == ".json"
	}}
	rec, err := fetchSource(context.Background(), srv.Client(), src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if got := listFiles(t, dest); len(got) != 1 || got[0] != "compute/v1/compute-api.json" {
		t.Fatalf("files = %v", got)
	}
	if rec.Files != 1 {
		t.Fatalf("record = %+v", rec)
	}
	if entries, _ := os.ReadDir(filepath.Dir(dest)); len(entries) != 1 {
		t.Fatalf("zip spool file not removed: %v", entries)
	}
}

func TestFetchModuleZip_PrefersLocalDir(t *testing.T) {
	local := t.TempDir()
	if err := os.MkdirAll(filepath.Join(local, "compute", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(local, "compute", "v1", "compute-api.json"), []byte("{}"), 0o644)
	_ = os.WriteFile(filepath.Join(local, "compute", "v1", "compute-gen.go"), []byte("x"), 0o644)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++; w.WriteHeader(500) }))
	defer srv.Close()
	dest := t.TempDir()
	src := FetchSource{Name: "docs", Kind: KindModuleZip, URL: srv.URL, LocalDir: local, Keep: func(p string) bool {
		return filepath.Ext(p) == ".json"
	}}
	rec, err := fetchSource(context.Background(), srv.Client(), src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 0 || rec.Files != 1 || rec.URL != "file://"+local {
		t.Fatalf("hits=%d record=%+v", hits, rec)
	}
}

func TestFetchJSONIndex_ExpandsAndRejectsUnsafeNames(t *testing.T) {
	mux := http.NewServeMux()
	var srvURL string
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{{"service": "ec2", "url": srvURL + "/v1/ec2.json"}, {"service": "s3", "url": srvURL + "/v1/s3.json"}})
	})
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Name":"` + filepath.Base(r.URL.Path) + `"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL
	expand := func(index []byte) ([]IndexEntry, error) {
		var in []struct{ Service, URL string }
		if err := json.Unmarshal(index, &in); err != nil {
			return nil, err
		}
		out := make([]IndexEntry, 0, len(in))
		for _, e := range in {
			out = append(out, IndexEntry{Name: e.Service, URL: e.URL})
		}
		return out, nil
	}
	dest := t.TempDir()
	rec, err := fetchSource(context.Background(), srv.Client(), FetchSource{Name: "sr", Kind: KindJSONIndex, URL: srv.URL + "/", Expand: expand}, dest)
	if err != nil {
		t.Fatal(err)
	}
	got := listFiles(t, dest)
	want := []string{"ec2.json", "index.json", "s3.json"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("files = %v", got)
	}
	if rec.Files != 3 {
		t.Fatalf("record = %+v", rec)
	}

	// An entry named "index" would overwrite the index document beside it.
	shadow := func([]byte) ([]IndexEntry, error) {
		return []IndexEntry{{Name: indexName, URL: srv.URL + "/v1/ec2.json"}}, nil
	}
	if _, err := fetchSource(context.Background(), srv.Client(), FetchSource{Name: "sr", Kind: KindJSONIndex, URL: srv.URL + "/", Expand: shadow}, t.TempDir()); err == nil {
		t.Error("index entry shadowing the index document accepted")
	}

	// Two entries writing one file would race; the index is rejected whole.
	dup := func([]byte) ([]IndexEntry, error) {
		return []IndexEntry{{Name: "ec2", URL: srv.URL + "/v1/ec2.json"}, {Name: "ec2", URL: srv.URL + "/v1/ec2.json"}}, nil
	}
	if _, err := fetchSource(context.Background(), srv.Client(), FetchSource{Name: "sr", Kind: KindJSONIndex, URL: srv.URL + "/", Expand: dup}, t.TempDir()); err == nil {
		t.Error("duplicate index entry name accepted")
	}

	for _, bad := range []string{"../x", "..", ".", "a/b", `a\b`, "", strings.Repeat("x", 201), ".hidden"} {
		unsafe := func([]byte) ([]IndexEntry, error) { return []IndexEntry{{Name: bad, URL: srv.URL + "/v1/x.json"}}, nil }
		if _, err := fetchSource(context.Background(), srv.Client(), FetchSource{Name: "sr", Kind: KindJSONIndex, URL: srv.URL + "/", Expand: unsafe}, t.TempDir()); err == nil {
			t.Errorf("unsafe index entry name %q accepted", bad)
		}
	}
}

func TestFetchSource_HTTPErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }))
	defer srv.Close()
	_, err := fetchSource(context.Background(), srv.Client(), FetchSource{Name: "x", Kind: KindTarball, URL: srv.URL}, t.TempDir())
	if err == nil {
		t.Fatal("expected error on 404")
	}
}

// A server that accepts the request and then stops sending must fail the
// fetch, not hang: the old client had no timeout of any kind.
func TestHTTPGet_StalledBodyFails(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1024")
		_, _ = w.Write([]byte("start"))
		w.(http.Flusher).Flush()
		<-release // never sends the rest
	}))
	defer func() { close(release); srv.Close() }()

	body, err := httpGetIdle(context.Background(), srv.Client(), srv.URL, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("httpGet: %v", err)
	}
	defer body.Close()
	_, err = io.ReadAll(body)
	if err == nil {
		t.Fatal("read of a stalled body returned nil error")
	}
	if !strings.Contains(err.Error(), "transfer stalled") {
		t.Errorf("stalled read error = %v, want it to name the stall", err)
	}
}
