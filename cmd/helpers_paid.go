//go:build paid

package cmd

import (
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
)

// resolveCheckRunID expands the `latest` shorthand to the most-recent
// check_run's ID; literal IDs pass through after a presence check. Mirrors
// resolveScanID. Used by `disco findings list --check-run-id <...>`.
func resolveCheckRunID(db *store.Store, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw == "latest" {
		runs, err := db.ListCheckRuns()
		if err != nil {
			return "", fmt.Errorf("list check_runs: %w", err)
		}
		if len(runs) == 0 {
			return "", fmt.Errorf("no check runs recorded; --check-run-id latest has nothing to resolve")
		}
		return runs[0].ID, nil
	}
	if _, err := db.GetCheckRun(raw); err != nil {
		return "", fmt.Errorf("check run %q not found", raw)
	}
	return raw, nil
}
