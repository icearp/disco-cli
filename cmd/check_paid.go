//go:build paid

package cmd

import (
	"fmt"

	"codeberg.org/icearp/disco/internal/license"
	"codeberg.org/icearp/disco/internal/policy"
	"codeberg.org/icearp/disco/internal/store"
)

var checkPersist bool

func init() {
	checkCmd.Flags().BoolVar(&checkPersist, "persist", false,
		"Write the check run and its findings to the local DB; surfaces under `disco findings`")

	persistCheckHook = func(db *store.Store, paths, packs []string, severity string, resourceCount int, findings []policy.Finding) error {
		if !checkPersist {
			return nil
		}
		if err := license.Require(); err != nil {
			return err
		}
		if dbReadOnly {
			return fmt.Errorf("--persist cannot run in read-only mode")
		}
		rows := make([]store.StoredFinding, 0, len(findings))
		for _, f := range findings {
			rows = append(rows, findingToStored(f))
		}
		if _, err := db.PersistCheckRun(paths, packs, severity, resourceCount, rows); err != nil {
			return fmt.Errorf("persist check run: %w", err)
		}
		return nil
	}
}
