// Package util provides small helper functions shared across provider packages.
package util

import (
	"encoding/json"
	"math"
	"time"
)

// AllResources is a query limit large enough to fetch every resource of a type
// in one call. Used by relationship resolvers that need all resources in memory.
const AllResources = uint64(math.MaxUint32)

// MustJSON marshals v to a JSON string. Returns "{}" if marshalling fails —
// this should never happen for well-formed SDK response structs.
func MustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Sv dereferences a string pointer, returning "" for nil.
func Sv(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// TimeRFC3339 formats a *time.Time as an RFC3339 string pointer.
// Returns nil when t is nil.
func TimeRFC3339(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
