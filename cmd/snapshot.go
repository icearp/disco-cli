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

var (
	snapshotForce          bool
	snapshotSigningPayload string
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot <output-file>",
	Short: "Freeze the local DB into a single-file evidence package",
	Long: `Writes <output-file> — a single archive (zip, tar.gz/tgz, or
tar.xz/txz, format inferred from extension) containing disco.db (atomic
copy via SQLite VACUUM INTO) plus manifest.json (tool_version,
db_sha256, generated_at, scan_ids). Pair with 'disco verify
<output-file>' on the receiving end.

Refuses to overwrite an existing file unless --force. Writes via a
temporary sibling file and atomically renames, so a failed snapshot
leaves no partial archive at the output path. --db-readonly is allowed
(the source DB is opened read-only). Output is silent on stdout; a
single-line summary lands on stderr.

Examples:
  disco snapshot /tmp/audit-2026-q2.tar.xz
  disco snapshot /tmp/audit-2026-q2.zip --force
  disco --db-readonly snapshot /tmp/handoff.tgz

## Signing

Pass --signing-payload <file> to write the canonical (RFC 8785-style
JCS) bytes of the manifest alongside the archive. Sign that payload
externally with any tool that produces a raw 64-byte ed25519 signature
(minisign, cosign sign-blob, openssl pkeyutl -sign -rawin) and ship the
detached signature plus an ed25519 public key to the receiver, who runs
'disco verify --signature <sig> --pubkey <key>'.

The pubkey may be PEM (openssl pkey -pubout), an OpenSSH .pub line
(ssh-keygen -t ed25519), or a raw 32-byte file. NOTE: ssh-keygen -Y
sign produces an SSHSIG-armored envelope, NOT a raw ed25519 signature,
so its output is incompatible with --signature. Use openssl / cosign /
minisign for the signing step even when reusing an SSH ed25519 key as
the verifier identity.

Canonical OpenSSL recipe end-to-end:

  openssl genpkey -algorithm ed25519 -out priv.pem
  openssl pkey -in priv.pem -pubout -out pub.pem
  disco snapshot evidence.tgz --signing-payload payload
  openssl pkeyutl -sign -inkey priv.pem -rawin -in payload -out evidence.sig
  disco verify evidence.tgz --signature evidence.sig --pubkey pub.pem

Cosign/Sigstore-witnessed signing (transparency log inclusion) ships in
a paid follow-up. The OSS plumbing here (canonical payload + ed25519
detached) closes the unsigned-manifest forgery gap reported in
focus-group/SUMMARY.md F1.

Signing example:
  disco snapshot /tmp/audit.tgz --signing-payload /tmp/audit.manifest.json`,
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

		if snapshotSigningPayload != "" {
			payload, err := snapshot.CanonicalManifestBytes(m)
			if err != nil {
				return err
			}
			if err := os.WriteFile(snapshotSigningPayload, payload, 0o600); err != nil {
				return fmt.Errorf("write signing payload: %w", err)
			}
		}

		abs, _ := filepath.Abs(out)
		fmt.Fprintf(os.Stderr, "Wrote snapshot to %s (sha256=%s, scans=%d, format=%s)\n", abs, short(hash), len(ids), format)
		return nil
	},
}

func init() {
	snapshotCmd.Flags().BoolVar(&snapshotForce, "force", false, "Overwrite the output file if it already exists")
	snapshotCmd.Flags().StringVar(&snapshotSigningPayload, "signing-payload", "",
		"Write the canonical manifest bytes to this path so an external tool can produce a detached ed25519 signature")
	rootCmd.AddCommand(snapshotCmd)
}
