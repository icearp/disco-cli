package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func resetScansFlags() {
	scansOutputFmt = ""
}

// TestRenderScans_EmptyJSON pins the wire contract: a zero-row scan list
// renders as `[]`, not `null`, so strict consumers (jq '.[]') don't break.
func TestRenderScans_EmptyJSON(t *testing.T) {
	out, err := captureStdout(t, func() error { return renderScans(nil, "json") })
	if err != nil {
		t.Fatalf("renderScans: %v", err)
	}
	if got := strings.TrimSpace(out); got != "[]" {
		t.Errorf("empty scans -o json: want [], got %q", got)
	}
}

func TestScans_List(t *testing.T) {
	st := seedTestDB(t) // creates one scan + 2 rows
	if _, err := st.CreateScan([]string{"aws", "gcp"}, map[string]any{"regions": []string{"us-east-1"}}); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	resetScansFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"scans"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("scans: %v", err)
	}
	if !strings.Contains(out, "STARTED") || !strings.Contains(out, "PROVIDERS") {
		t.Errorf("missing header: %s", out)
	}
	// Two scan rows (one from seedTestDB + one fresh) + header line.
	if lines := strings.Count(out, "\n"); lines < 3 {
		t.Errorf("want >=3 lines, got %d:\n%s", lines, out)
	}
	if !strings.Contains(out, "aws,gcp") {
		t.Errorf("providers join missing: %s", out)
	}
}

func TestScans_ListJSON(t *testing.T) {
	seedTestDB(t)
	resetScansFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"scans", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("scans: %v", err)
	}
	var scans []store.Scan
	if jerr := json.Unmarshal([]byte(out), &scans); jerr != nil {
		t.Fatalf("decode: %v\n%s", jerr, out)
	}
	if len(scans) != 1 {
		t.Fatalf("want 1 scan, got %d", len(scans))
	}
	if len(scans[0].Providers) != 1 || scans[0].Providers[0] != "aws" {
		t.Errorf("providers slice: got %v, want [aws]", scans[0].Providers)
	}
}

func TestScans_ShowLatest(t *testing.T) {
	seedTestDB(t)
	resetScansFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"scans", "show", "latest"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("show latest: %v", err)
	}
	for _, want := range []string{"ID:", "Status:", "Providers:", "aws"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestScans_ShowUnknown(t *testing.T) {
	seedTestDB(t)
	resetScansFlags()

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"scans", "show", "deadbeef"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("want 'not found' err, got %v", err)
	}
}

func TestScans_ShowJSON(t *testing.T) {
	seedTestDB(t)
	resetScansFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"scans", "show", "latest", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var sc store.Scan
	if jerr := json.Unmarshal([]byte(out), &sc); jerr != nil {
		t.Fatalf("decode: %v\n%s", jerr, out)
	}
	if sc.ID == "" || len(sc.Providers) == 0 {
		t.Errorf("decoded shape: %+v", sc)
	}
}
