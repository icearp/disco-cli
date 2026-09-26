package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestCoverageSDKStatus_EmptyCache: status on an empty cache lists every
// registered extractor as absent, with the ref it would fetch, and never
// touches the network.
func TestCoverageSDKStatus_EmptyCache(t *testing.T) {
	resetCoverageFlags(t)
	root := filepath.Join(t.TempDir(), "sdk")
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "sdk", "status", "--sdk-cache", root, "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatal(err)
	}
	var rows []sdkStatusRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (aws, azure, gcp)", len(rows))
	}
	for _, r := range rows {
		if r.Present || r.Ref == "" || !strings.HasPrefix(r.Dir, root) {
			t.Errorf("row %+v", r)
		}
	}
}

func TestCoverageSDKFetch_UnknownProvider(t *testing.T) {
	resetCoverageFlags(t)
	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"coverage", "sdk", "fetch", "--providers", "oci", "--sdk-cache", t.TempDir()})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), `unknown provider "oci"`) {
		t.Fatalf("err = %v", err)
	}
}
