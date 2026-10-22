// Package util provides small helper functions shared across provider packages.
package util

import (
	"encoding/json"
	"math"
	"strings"
	"time"
)

// AllResources is a query limit large enough to fetch every resource of a type
// in one call, for resolvers that need all resources in memory.
const AllResources = uint64(math.MaxUint32)

// marshalFailedKey names the only member of the object MustJSON returns when
// v does not marshal. Nothing stops a value from carrying this key itself (a
// map, or a struct field tagged with it), so IsMarshalFailure also requires
// it to be the object's only member.
const marshalFailedKey = "disco:marshalError"

const marshalFailedPrefix = `{"` + marshalFailedKey + `":`

// MustJSON marshals v to a JSON string. When v does not marshal (a NaN or
// Inf float, a failing MarshalJSON) it returns {"disco:marshalError":"<err>"}
// instead, which IsMarshalFailure recognises. It does not return an error
// because every scanner row is built through it, and it does not panic
// because one bad row must not abort a scan: the store reports a row carrying
// this object as a scan error when it is written.
func MustJSON(v any) string {
	b, err := json.Marshal(v)
	if err == nil {
		return string(b)
	}
	// The error is discarded because a map[string]string always marshals.
	b, _ = json.Marshal(map[string]string{marshalFailedKey: err.Error()})
	return string(b)
}

// IsMarshalFailure reports whether s is the object MustJSON returns when its
// argument did not marshal: exactly one member, named marshalFailedKey. The
// prefix check keeps the decode off every ordinary row the store writes.
func IsMarshalFailure(s string) bool {
	if !strings.HasPrefix(s, marshalFailedPrefix) {
		return false
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &members); err != nil {
		return false // MustJSON never returns malformed JSON
	}
	_, ok := members[marshalFailedKey]
	return ok && len(members) == 1
}

// Sv dereferences a string pointer, returning "" for nil.
func Sv(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// TimeRFC3339 formats a *time.Time as an RFC3339 string pointer, or nil when
// t is nil.
func TimeRFC3339(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
