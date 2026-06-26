package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestStaticCompletion(t *testing.T) {
	fn := staticCompletion("table", "json")
	got, dir := fn(nil, nil, "")
	if len(got) != 2 || got[0] != "table" || got[1] != "json" {
		t.Errorf("values = %v; want [table json]", got)
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v; want NoFileComp (no filename completion)", dir)
	}
}

// TestOutputCompletion_Integration drives cobra's hidden __complete command so
// a registration attached to the wrong flag name (which RegisterFlagCompletionFunc
// would silently no-op on) is caught end-to-end, unlike the helper unit tests.
func TestOutputCompletion_Integration(t *testing.T) {
	out, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"__complete", "list", "-o", ""})
		return rootCmd.Execute()
	})
	if err != nil {
		t.Fatalf("__complete list -o: %v", err)
	}
	if !strings.Contains(out, "json") || !strings.Contains(out, "table") {
		t.Errorf("want format suggestions for list -o, got:\n%s", out)
	}
	// ShellCompDirectiveNoFileComp == 4; its absence means file completion leaked.
	if !strings.Contains(out, ":4") {
		t.Errorf("want NoFileComp directive (:4), got:\n%s", out)
	}
}

func TestCompleteProviderNames(t *testing.T) {
	got, dir := completeProviderNames(nil, nil, "")
	// The internal/providers/all blank import registers aws/azure/gcp.
	if len(got) == 0 {
		t.Fatal("expected registered provider names, got none")
	}
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v; want NoFileComp", dir)
	}
	seen := map[string]bool{}
	for _, p := range got {
		seen[p] = true
	}
	if !seen["aws"] {
		t.Errorf("provider completion missing aws; got %v", got)
	}
}
