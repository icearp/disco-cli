package gcpinventory

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestPinMatchesGoMod: the fallback pin must track the version go.mod
// resolves, or a dependabot bump would silently read stale Discovery docs in
// binaries that do not link google.golang.org/api.
func TestPinMatchesGoMod(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*google\.golang\.org/api (v[0-9.]+)`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("google.golang.org/api not found in go.mod")
	}
	if got := string(m[1]); got != APIRef {
		t.Fatalf("APIRef = %q, go.mod pins %q — update gcpinventory/pins.go", APIRef, got)
	}
	if (extractor{}).Ref() != APIRef {
		t.Fatalf("Ref() = %q in a binary without the module; want fallback %q", (extractor{}).Ref(), APIRef)
	}
}

func TestFetchSpec(t *testing.T) {
	spec := (extractor{}).FetchSpec()
	if len(spec) != 1 || spec[0].Strip != 2 || spec[0].LocalDir == "" {
		t.Fatalf("spec = %+v", spec)
	}
	if !spec[0].Keep("compute/v1/compute-api.json") || spec[0].Keep("compute/v1/compute-gen.go") {
		t.Fatal("Keep filter wrong")
	}
}
