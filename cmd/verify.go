package cmd

import (
	"fmt"
	"os"

	"codeberg.org/icearp/disco/internal/snapshot"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:   "verify <archive-file>",
	Short: "Verify a disco-snapshot archive",
	Long: `Reads the archive at <archive-file>, decodes manifest.json from it,
recomputes the SHA-256 of the embedded disco.db, and compares against
manifest.db_sha256. Exits 0 on match, non-zero with a clear stderr
message on any mismatch / missing entry / format error.

Verifies unsigned archives written by 'disco snapshot'. Signed-manifest
verification (cosign/Sigstore) ships in a paid follow-up.

Examples:
  disco verify /tmp/audit-2026-q2.tar.xz`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		path := args[0]
		m, computed, err := snapshot.ArchiveContents(path)
		if err != nil {
			return err
		}
		if m.Format != snapshot.FormatV1 {
			return fmt.Errorf("unsupported manifest format %q (want %q)", m.Format, snapshot.FormatV1)
		}
		if computed != m.DBSHA256 {
			return fmt.Errorf("verification failed: db_sha256 mismatch (manifest=%s, computed=%s)", m.DBSHA256, computed)
		}
		fmt.Fprintf(os.Stdout, "OK: %s (tool_version=%s, sha256=%s, scans=%d, generated_at=%s)\n",
			path, m.ToolVersion, short(computed), len(m.ScanIDs), m.GeneratedAt)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(verifyCmd)
}
