//go:build !paid

package store

// paidBuild is the OSS-build value of the migration runner's
// paid-filter gate. False means `*_paid.sql` migrations are skipped.
const paidBuild = false
