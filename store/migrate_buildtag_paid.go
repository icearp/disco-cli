//go:build paid

package store

// paidBuild is the paid-build value of the migration runner's
// paid-filter gate. True means `*_paid.sql` migrations apply.
const paidBuild = true
