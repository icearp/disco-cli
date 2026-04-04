// Package util provides small helper functions shared across provider packages.
package util

import (
	"encoding/json"
	"math"
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
