package snapshot

import (
	"fmt"
	"strings"
)

// Format identifies the on-disk archive container for a disco snapshot.
// FormatUnknown is the zero value; callers must reject it.
type Format int

const (
	FormatUnknown Format = iota
	FormatZip
	FormatTarGz
	FormatTarXz
)

// String returns the canonical extension prefix for the format.
func (f Format) String() string {
	switch f {
	case FormatZip:
		return "zip"
	case FormatTarGz:
		return "tar.gz"
	case FormatTarXz:
		return "tar.xz"
	}
	return "unknown"
}

// SupportedFormats lists the file-extension shapes accepted by DetectFormat.
// Order is stable for help-text rendering.
var SupportedFormats = []string{".zip", ".tar.gz", ".tgz", ".tar.xz", ".txz"}

// DetectFormat returns the archive format inferred from the path's
// extension. Match is case-insensitive. Unknown extensions surface a
// clear error listing supported shapes.
func DetectFormat(path string) (Format, error) {
	low := strings.ToLower(path)
	switch {
	case strings.HasSuffix(low, ".zip"):
		return FormatZip, nil
	case strings.HasSuffix(low, ".tar.gz"), strings.HasSuffix(low, ".tgz"):
		return FormatTarGz, nil
	case strings.HasSuffix(low, ".tar.xz"), strings.HasSuffix(low, ".txz"):
		return FormatTarXz, nil
	}
	return FormatUnknown, fmt.Errorf("unsupported snapshot format for %q (supported: %s)", path, strings.Join(SupportedFormats, ", "))
}
