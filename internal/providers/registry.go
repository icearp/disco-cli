package providers

import (
	"context"
	"sort"

	"codeburg.org/icearp/disco/internal/store"
)

// Scanner is the interface every cloud provider must implement.
// Each provider package registers itself via init() by calling Register.
type Scanner interface {
	// Name returns the provider's canonical identifier (e.g. "aws", "azure", "gcp").
	Name() string
	// Scan discovers all resources for this provider and persists them via st.
	// scanID ties every upserted resource and relationship to this scan run.
	Scan(ctx context.Context, st *store.Store, scanID string) error
}

// registry maps provider name → Scanner. Populated by provider init() calls.
var registry = map[string]Scanner{}

// Register adds a scanner to the global registry.
// Providers call this from their package init() function.
func Register(s Scanner) {
	registry[s.Name()] = s
}

// Get returns the scanner for the given provider name, if registered.
func Get(name string) (Scanner, bool) {
	s, ok := registry[name]
	return s, ok
}

// All returns all registered scanners sorted by name.
func All() []Scanner {
	scanners := make([]Scanner, 0, len(registry))
	for _, s := range registry {
		scanners = append(scanners, s)
	}
	sort.Slice(scanners, func(i, j int) bool {
		return scanners[i].Name() < scanners[j].Name()
	})
	return scanners
}

// Names returns the names of all registered providers, sorted.
// Used for error messages and scan metadata.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
