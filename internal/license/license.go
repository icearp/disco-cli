//go:build !paid

// Package license gates paid-only commands and features. The OSS build
// always reports unlicensed; the paid build (`-tags paid`) replaces this
// stub via license_paid.go.
package license

import "errors"

// ErrNotLicensed is returned by paid command guards when running under the
// OSS build. Callers should surface it to cobra so the CLI exits non-zero
// with a clear message.
var ErrNotLicensed = errors.New("this command requires a paid build of disco")

// IsLicensed reports whether the running binary has paid features enabled.
func IsLicensed() bool { return false }

// Require returns ErrNotLicensed when the binary is not licensed. Paid
// command RunE functions should call this first.
func Require() error { return ErrNotLicensed }
