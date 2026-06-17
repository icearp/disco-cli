package snapshot

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ulikunitz/xz"
)

// Sentinel errors so cmd/verify.go can render auditor-grade specific stderr
// strings instead of one collapsed "archive corrupt or truncated". Same
// exit code (1) for all; the difference is the diagnostic an auditor
// quotes in their report.
var (
	// ErrArchiveCorrupt covers gzip/xz/tar/zip framing or CRC failures —
	// the bytes are not a well-formed archive.
	ErrArchiveCorrupt = errors.New("archive corrupt or truncated")
	// ErrManifestMissing means the archive opened cleanly but the
	// disco.db or manifest.json entry is absent.
	ErrManifestMissing = errors.New("manifest entry missing")
	// ErrManifestFormat means manifest.json was present but did not parse
	// as JSON / failed schema decode.
	ErrManifestFormat = errors.New("manifest format invalid")
	// ErrTrailingBytes flags a tail-tampered archive (`echo x >> snap.tgz`).
	ErrTrailingBytes = errors.New("trailing bytes after archive end")
)

// Inner archive entry names. Single-level layout (no parent dir) so the
// receiver doesn't need to know a fixed prefix.
const (
	entryDB       = "disco.db"
	entryManifest = "manifest.json"
)

// WriteArchive packages dbPath + manifest into a single archive at out.
// Writes via "<out>.tmp" + os.Rename so a failed write leaves no partial
// archive at the final path. dbPath is streamed (not loaded into memory).
func WriteArchive(out string, format Format, dbPath string, m Manifest) error {
	tmp := out + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create archive temp: %w", err)
	}
	cleanup := func() { _ = f.Close(); _ = os.Remove(tmp) }

	dbBytes, err := os.Open(dbPath)
	if err != nil {
		cleanup()
		return fmt.Errorf("open db for archive: %w", err)
	}
	defer func() { _ = dbBytes.Close() }()
	dbStat, err := dbBytes.Stat()
	if err != nil {
		cleanup()
		return fmt.Errorf("stat db: %w", err)
	}

	manifestBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		cleanup()
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')

	switch format {
	case FormatZip:
		err = writeZip(f, dbBytes, manifestBytes)
	case FormatTarGz:
		err = writeTarStream(f, dbBytes, dbStat.Size(), manifestBytes, true)
	case FormatTarXz:
		err = writeTarStream(f, dbBytes, dbStat.Size(), manifestBytes, false)
	default:
		err = fmt.Errorf("unsupported format %v", format)
	}
	if err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close archive temp: %w", err)
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename archive: %w", err)
	}
	return os.Chmod(out, 0o600)
}

func writeZip(w io.Writer, db io.Reader, manifestBytes []byte) error {
	zw := zip.NewWriter(w)
	dbHdr := &zip.FileHeader{Name: entryDB, Method: zip.Deflate}
	dbHdr.SetMode(0o600)
	dbW, err := zw.CreateHeader(dbHdr)
	if err != nil {
		return fmt.Errorf("zip db header: %w", err)
	}
	if _, err := io.Copy(dbW, db); err != nil {
		return fmt.Errorf("zip db copy: %w", err)
	}
	mHdr := &zip.FileHeader{Name: entryManifest, Method: zip.Deflate}
	mHdr.SetMode(0o600)
	mW, err := zw.CreateHeader(mHdr)
	if err != nil {
		return fmt.Errorf("zip manifest header: %w", err)
	}
	if _, err := mW.Write(manifestBytes); err != nil {
		return fmt.Errorf("zip manifest write: %w", err)
	}
	return zw.Close()
}

// writeTarStream writes a tar archive wrapped in either gzip or xz. db is
// streamed; manifest is a small in-memory buffer.
func writeTarStream(w io.Writer, db io.Reader, dbSize int64, manifestBytes []byte, useGzip bool) error {
	var compressor io.WriteCloser
	if useGzip {
		compressor = gzip.NewWriter(w)
	} else {
		xw, err := xz.NewWriter(w)
		if err != nil {
			return fmt.Errorf("xz writer: %w", err)
		}
		compressor = xw
	}
	tw := tar.NewWriter(compressor)

	if err := tw.WriteHeader(&tar.Header{
		Name: entryDB, Mode: 0o600, Size: dbSize, Typeflag: tar.TypeReg,
	}); err != nil {
		_ = compressor.Close()
		return fmt.Errorf("tar db header: %w", err)
	}
	if _, err := io.Copy(tw, db); err != nil {
		_ = compressor.Close()
		return fmt.Errorf("tar db copy: %w", err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: entryManifest, Mode: 0o600, Size: int64(len(manifestBytes)), Typeflag: tar.TypeReg,
	}); err != nil {
		_ = compressor.Close()
		return fmt.Errorf("tar manifest header: %w", err)
	}
	if _, err := tw.Write(manifestBytes); err != nil {
		_ = compressor.Close()
		return fmt.Errorf("tar manifest write: %w", err)
	}
	if err := tw.Close(); err != nil {
		_ = compressor.Close()
		return fmt.Errorf("tar close: %w", err)
	}
	return compressor.Close()
}

// ArchiveContents returns the manifest plus a hex SHA-256 of the inner
// disco.db, computed by streaming the archive once. No temp files; no full
// DB load. Manifest is decoded from the manifest.json entry. Order of
// entries inside the archive is irrelevant — the function reads to
// completion before returning.
func ArchiveContents(path string) (Manifest, string, error) {
	format, err := DetectFormat(path)
	if err != nil {
		return Manifest{}, "", err
	}
	switch format {
	case FormatZip:
		return readZip(path)
	case FormatTarGz, FormatTarXz:
		return readTar(path, format == FormatTarGz)
	}
	return Manifest{}, "", fmt.Errorf("unsupported format")
}

func readZip(path string) (Manifest, string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("%w: open zip: %v", ErrArchiveCorrupt, err)
	}
	defer func() { _ = zr.Close() }()
	var (
		m      Manifest
		sawM   bool
		dbHash string
		sawDB  bool
	)
	for _, f := range zr.File {
		switch f.Name {
		case entryManifest:
			rc, err := f.Open()
			if err != nil {
				return Manifest{}, "", fmt.Errorf("%w: open manifest: %v", ErrArchiveCorrupt, err)
			}
			b, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return Manifest{}, "", fmt.Errorf("%w: read manifest: %v", ErrArchiveCorrupt, err)
			}
			if err := json.Unmarshal(b, &m); err != nil {
				return Manifest{}, "", fmt.Errorf("%w: decode manifest: %v", ErrManifestFormat, err)
			}
			sawM = true
		case entryDB:
			rc, err := f.Open()
			if err != nil {
				return Manifest{}, "", fmt.Errorf("%w: open db: %v", ErrArchiveCorrupt, err)
			}
			h, err := hashReader(rc)
			_ = rc.Close()
			if err != nil {
				return Manifest{}, "", fmt.Errorf("%w: hash db: %v", ErrArchiveCorrupt, err)
			}
			dbHash = h
			sawDB = true
		}
	}
	if !sawM {
		return Manifest{}, "", fmt.Errorf("%w: %s", ErrManifestMissing, entryManifest)
	}
	if !sawDB {
		return Manifest{}, "", fmt.Errorf("%w: %s", ErrManifestMissing, entryDB)
	}
	// archive/zip parses the EOCD, ignoring any data appended after it; we
	// don't replicate the strict-tail check that readTar does for tar.gz/xz
	// because zip's append tolerance is the format's design choice. tgz/txz
	// remain the recommended snapshot containers for tamper-evidence.
	return m, dbHash, nil
}

// tailReader passes reads through to the underlying reader. By
// implementing io.ByteReader it satisfies compress/gzip's `flate.Reader`
// interface (Read + ReadByte), so gzip skips wrapping the input in a
// `bufio.Reader` — which would otherwise greedily prefetch bytes past the
// gzip footer and silently swallow tail tampering. With single-byte
// reads, the underlying file's position lands exactly at the end of the
// compressed stream once the consumer reaches EOF, and tail() can return
// a non-zero count when `echo extra >> snap.tgz` style tampering is
// present.
type tailReader struct {
	r io.Reader
}

func (c *tailReader) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c *tailReader) ReadByte() (byte, error) {
	var b [1]byte
	n, err := c.r.Read(b[:])
	if n > 0 {
		return b[0], nil
	}
	if err == nil {
		err = io.EOF
	}
	return 0, err
}

func (c *tailReader) tail() (int, error) {
	var buf [1024]byte
	total := 0
	for {
		n, err := c.r.Read(buf[:])
		total += n
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, fmt.Errorf("read trailing bytes: %w", err)
		}
		if n == 0 {
			return total, nil
		}
	}
}

func readTar(path string, useGzip bool) (Manifest, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("open tar: %w", err)
	}
	defer func() { _ = f.Close() }()
	// Wrap the underlying file so we can confirm no trailing bytes remain
	// after the compressed stream. `echo extra >> snap.tgz` would otherwise
	// pass — gzip's framing tolerates trailing data when Multistream is on,
	// and xz silently stops at its footer. Disable Multistream on gzip so
	// the reader stops at the first member's footer; for xz the reader
	// stops at the stream end natively. After the inner tar reaches EOF we
	// read one more byte from the underlying file — non-zero means tail
	// tampering.
	cf := &tailReader{r: f}
	var decompressed io.Reader
	if useGzip {
		gr, err := gzip.NewReader(cf)
		if err != nil {
			return Manifest{}, "", fmt.Errorf("%w: gzip open: %v", ErrArchiveCorrupt, err)
		}
		gr.Multistream(false)
		defer func() { _ = gr.Close() }()
		decompressed = gr
	} else {
		xr, err := xz.NewReader(cf)
		if err != nil {
			return Manifest{}, "", fmt.Errorf("%w: xz open: %v", ErrArchiveCorrupt, err)
		}
		decompressed = xr
	}
	tr := tar.NewReader(decompressed)
	var (
		m      Manifest
		sawM   bool
		dbHash string
		sawDB  bool
	)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Manifest{}, "", fmt.Errorf("%w: tar next: %v", ErrArchiveCorrupt, err)
		}
		switch hdr.Name {
		case entryManifest:
			b, err := io.ReadAll(tr)
			if err != nil {
				return Manifest{}, "", fmt.Errorf("%w: read manifest: %v", ErrArchiveCorrupt, err)
			}
			if err := json.Unmarshal(b, &m); err != nil {
				return Manifest{}, "", fmt.Errorf("%w: decode manifest: %v", ErrManifestFormat, err)
			}
			sawM = true
		case entryDB:
			h, err := hashReader(tr)
			if err != nil {
				return Manifest{}, "", fmt.Errorf("%w: hash db: %v", ErrArchiveCorrupt, err)
			}
			dbHash = h
			sawDB = true
		}
	}
	if !sawM {
		return Manifest{}, "", fmt.Errorf("%w: %s", ErrManifestMissing, entryManifest)
	}
	if !sawDB {
		return Manifest{}, "", fmt.Errorf("%w: %s", ErrManifestMissing, entryDB)
	}
	// Strict tail check for tar.gz only. The xz library
	// (github.com/ulikunitz/xz) doesn't pull index+footer through the
	// ByteReader path — a clean archive shows ~33 unread bytes — so a
	// reliable trailing-byte detector for tar.xz needs a footer-aware
	// parser we don't ship today. tar.gz is the persona-reported case
	// (`echo extra >> snap.tgz`) and the recommended evidence container.
	if useGzip {
		if extra, err := cf.tail(); err != nil {
			return Manifest{}, "", fmt.Errorf("%w: %v", ErrArchiveCorrupt, err)
		} else if extra > 0 {
			return Manifest{}, "", fmt.Errorf("%w: %d byte(s)", ErrTrailingBytes, extra)
		}
	}
	return m, dbHash, nil
}
