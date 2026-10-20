// Package untaggable records the disco resource types disco declares
// untaggable: rows the cloud gives no way to tag, such as aws:iam:group.
// `disco tag-coverage` leaves them out of its default denominator, because an
// untagged row of such a type is not a tagging gap the customer can close.
//
// Where disco's SDK extractor matches an AWS type to an AWS Service Reference
// resource, the type's declaration follows that resource and the SDK models:
// untaggable when the Service Reference lists no way to tag it and no SDK
// operation for it carries tags. TestUntaggableMatchesServiceReference in the
// aws package holds those declarations to that answer. The account settings
// the Service Reference does not list are declared by hand and held by
// TestUntaggableTypesDeclared. Every other type counts as taggable, including
// a Service Reference resource the extractor does not match to its type. A
// type is taggable unless its provider declares otherwise, so every type of a
// provider that declares nothing counts. The managed flag is a
// different question (who owns the row) and does not imply either answer.
//
// Provider packages register via the unified restype.Descriptor
// (Untaggable:true), which forwards here.
package untaggable

import (
	"slices"
	"sync"
)

var (
	mu       sync.RWMutex
	registry = map[string]bool{}
)

// Register marks resourceType as untaggable. Empty type is a no-op. Idempotent.
func Register(resourceType string) {
	if resourceType == "" {
		return
	}
	mu.Lock()
	registry[resourceType] = true
	mu.Unlock()
}

// Is reports whether resourceType was registered as untaggable.
func Is(resourceType string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return registry[resourceType]
}

// Types returns every registered untaggable type, sorted.
func Types() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}
