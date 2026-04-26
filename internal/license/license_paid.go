//go:build paid

package license

import "errors"

// ErrNotLicensed is unused in the paid build's happy path but kept for
// API symmetry with the OSS stub. Future signed-token validation will
// return it on expired or invalid licenses.
var ErrNotLicensed = errors.New("license invalid or expired")

// IsLicensed reports whether the running binary has paid features
// enabled. The current paid build returns true unconditionally;
// signed-token validation lands when the licensing scheme ships.
func IsLicensed() bool { return true }

// Require returns nil when the binary is licensed.
func Require() error { return nil }
