// Package managed records disco resource types that are UNCONDITIONALLY
// provider-managed — created automatically by the cloud and not user-deletable
// (Azure built-in policy/role definitions, AWS-owned prefix lists,
// per-(account,region) singleton config rows, ...). Store.UpsertResources
// consults it at the store boundary and stamps Resource.ManagedByProvider,
// mirroring how redact/volatile apply per-type rules there.
//
// This registry is for the always-managed case only. Types whose managed
// status depends on a per-row SDK field (OwnerId=="AWS", PolicyType==Managed,
// name prefixes) are NOT registered here — their scanner sets
// ManagedByProvider per row. A type is one or the other, never both.
//
// Provider packages register via the unified restype.Descriptor (Managed:true),
// which forwards here — they never call managed.Register directly.
package managed

import "sync"

var (
	mu       sync.RWMutex
	registry = map[string]bool{}
)

// Register marks resourceType as unconditionally provider-managed. Empty type
// is a no-op. Idempotent.
func Register(resourceType string) {
	if resourceType == "" {
		return
	}
	mu.Lock()
	registry[resourceType] = true
	mu.Unlock()
}

// Is reports whether resourceType was registered as unconditionally managed.
func Is(resourceType string) bool {
	mu.RLock()
	defer mu.RUnlock()
	return registry[resourceType]
}

// resetForTest wipes the registry. Test-only.
func resetForTest() {
	mu.Lock()
	registry = map[string]bool{}
	mu.Unlock()
}
