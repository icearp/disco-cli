package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"codeberg.org/icearp/disco/internal/snapshot"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

var snapshotForce bool

var snapshotCmd = &cobra.Command{
	Use:   "snapshot <output-file>",
	Short: "Freeze the local DB into a single-file evidence package",
	Long: `Writes <output-file> — a single archive (` + "`zip`" + `, ` + "`tar.gz`" + `/` + "`tgz`" + `,
or ` + "`tar.xz`" + `/` + "`txz`" + `) containing disco.db (atomic copy via SQLite VACUUM
INTO) plus manifest.json (tool_version, db_sha256, generated_at, scan_ids).
Pair with 'disco verify <output-file>' on the receiving end.

Format is inferred from the file extension. Unknown extensions error.

Refuses to overwrite an existing file unless --force. Writes via a
temporary sibling file and atomically renames so a failed snapshot
leaves no partial archive at the output path.

--db-readonly is allowed (the source DB is opened read-only; the output
file is its own write target). Output is silent on stdout; a single-line
summary lands on stderr.

Note: this build emits unsigned archives. Signed manifests
(cosign/Sigstore) ship in a paid follow-up.

Examples:
  disco snapshot /tmp/audit-2026-q2.tar.xz
  disco snapshot /tmp/audit-2026-q2.zip --force
  disco --db-readonly snapshot /tmp/handoff.tgz`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		out := args[0]
		format, err := snapshot.DetectFormat(out)
		if err != nil {
			return err
		}

		if !snapshotForce {
			if _, err := os.Stat(out); err == nil {
				return fmt.Errorf("%s already exists; pass --force to overwrite", out)
			}
		}

		src, err := store.OpenReadOnly(defaultDBPath())
		if err != nil {
			return fmt.Errorf("open source database: %w", err)
		}
		defer func() { _ = src.Close() }()

		// VACUUM INTO needs a non-existent target file. Place it next to the
		// final output (same filesystem) so the archive temp + rename stay
		// atomic on producer crashes.
		tmpDB := out + ".db.tmp"
		_ = os.Remove(tmpDB)
		defer func() { _ = os.Remove(tmpDB) }()
		if _, err := src.DB().Exec(fmt.Sprintf("VACUUM INTO %q", tmpDB)); err != nil {
			return fmt.Errorf("vacuum into snapshot: %w", err)
		}

		hash, err := snapshot.HashFile(tmpDB)
		if err != nil {
			return err
		}

		scans, err := src.ListScans()
		if err != nil {
			return fmt.Errorf("list scans: %w", err)
		}
		ids := make([]string, 0, len(scans))
		for _, s := range scans {
			ids = append(ids, s.ID)
		}
		sort.Strings(ids)

		m := snapshot.Manifest{
			Format:      snapshot.FormatV1,
			ToolVersion: Version,
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			DBSHA256:    hash,
			ScanIDs:     ids,
		}
		if err := snapshot.WriteArchive(out, format, tmpDB, m); err != nil {
			return err
		}

		abs, _ := filepath.Abs(out)
		fmt.Fprintf(os.Stderr, "Wrote snapshot to %s (sha256=%s, scans=%d, format=%s)\n", abs, short(hash), len(ids), format)
		return nil
	},
}

func init() {
	snapshotCmd.Flags().BoolVar(&snapshotForce, "force", false, "Overwrite the output file if it already exists")
	rootCmd.AddCommand(snapshotCmd)
}
