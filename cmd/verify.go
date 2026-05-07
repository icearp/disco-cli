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

By default, verifies unsigned archives written by 'disco snapshot' —
checks DB integrity against the embedded manifest only, NOT manifest
provenance. Anyone who can rewrite both halves can fabricate an "OK"
archive. Pair with --signature and --pubkey to verify a detached ed25519
signature over the canonical manifest bytes for true tamper-evidence.

Examples:
  disco verify /tmp/audit-2026-q2.tar.xz
  disco verify /tmp/audit.tgz --signature /tmp/audit.sig --pubkey /tmp/k.pub`,
	Args: cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		path := args[0]
		// Validate the format up-front so unsupported extensions surface a
		// "unsupported snapshot format" error instead of being collapsed into
		// "archive corrupt or truncated" by friendlyArchiveErr.
		if _, fmtErr := snapshot.DetectFormat(path); fmtErr != nil {
			return fmtErr
		}
		m, computed, err := snapshot.ArchiveContents(path)
		if err != nil {
			return friendlyArchiveErr(err)
		}
		if m.Format != snapshot.FormatV1 {
			return fmt.Errorf("unsupported manifest format %q (want %q)", m.Format, snapshot.FormatV1)
		}
		if computed != m.DBSHA256 {
			return fmt.Errorf("verification failed: db_sha256 mismatch (manifest=%s, computed=%s)", m.DBSHA256, computed)
		}

		signed := false
		if verifySigPath != "" || verifyPubKeyPath != "" {
			if verifySigPath == "" || verifyPubKeyPath == "" {
				return fmt.Errorf("--signature and --pubkey must be supplied together")
			}
			if err := snapshot.VerifyDetachedSignature(m, verifySigPath, verifyPubKeyPath); err != nil {
				return fmt.Errorf("verification failed: %w", err)
			}
			signed = true
		}

		prefix := "OK (unsigned — manifest not authenticated)"
		if signed {
			prefix = "OK (signed — manifest authenticated via ed25519)"
		}
		_, _ = fmt.Fprintf(os.Stdout, "%s: %s (tool_version=%s, sha256=%s, scans=%d, generated_at=%s)\n",
			prefix, path, m.ToolVersion, short(computed), len(m.ScanIDs), m.GeneratedAt)
		if !signed && m.ToolVersion == "dev" {
			_, _ = fmt.Fprintln(os.Stderr, "WARN: tool_version=dev — snapshot was built without a release version stamp")
		}
		return nil
	},
}

// friendlyArchiveErr converts a raw decoder error into a single
// "verify failed: archive corrupt or truncated" line so users at 4pm on a
// Friday don't see `hash: writeMatch: distance out of range` from the xz
// reader. The underlying message is preserved on --verbose.
func friendlyArchiveErr(err error) error {
	if err == nil {
		return nil
	}
	if verbose {
		return fmt.Errorf("verify failed: archive corrupt or truncated: %w", err)
	}
	return fmt.Errorf("verify failed: archive corrupt or truncated")
}

var (
	verifySigPath    string
	verifyPubKeyPath string
)

func init() {
	verifyCmd.Flags().StringVar(&verifySigPath, "signature", "", "Path to a detached ed25519 signature over the canonical manifest bytes")
	verifyCmd.Flags().StringVar(&verifyPubKeyPath, "pubkey", "", "Path to the ed25519 public key (PEM, OpenSSH, or 32-byte raw) that produced --signature")
	rootCmd.AddCommand(verifyCmd)
}
